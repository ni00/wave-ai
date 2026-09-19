// Notify signaling plane for the shared ORM pool.
//
// This is the one file in the service permitted to touch the PostgreSQL
// driver directly: pgx owns the dedicated LISTEN connection (it cannot run
// through the pooled gorm DB because LISTEN requires an idle connection that
// never returns to the pool), and the pg_notify publishers must execute on a
// caller's open transaction so the signal commits atomically with the state
// change that produced it. Everything else keeps using structured ORM calls.
package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Channel names for the two signaling planes. wave_task_queued wakes idle
// claim loops; wave_session_event wakes SSE streams watching one session.
const (
	ChannelTaskQueued   = "wave_task_queued"
	ChannelSessionEvent = "wave_session_event"
)

// channelPattern guards the channel names handed to LISTEN/UNLISTEN: the
// statements are built by concatenation, so only conservative identifiers
// are accepted.
var channelPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// NotifyQueued publishes the task-queue wake signal on the caller's open
// transaction. PostgreSQL discards notifications from aborted transactions,
// so the signal fires exactly when the enqueue it accompanies commits.
func NotifyQueued(tx *gorm.DB) error {
	return tx.Exec("SELECT pg_notify(?, '')", ChannelTaskQueued).Error
}

// NotifySessionEvent publishes a session-scoped wake signal on the caller's
// open transaction, delivered after commit. Consumers use the payload to
// match the notification to the stream they are serving.
func NotifySessionEvent(tx *gorm.DB, sessionID string) error {
	return tx.Exec("SELECT pg_notify(?, ?)", ChannelSessionEvent, sessionID).Error
}

// Listener multiplexes PostgreSQL LISTEN channels over one dedicated pgx
// connection. Subscribers receive notification payloads on their own buffered
// channel; delivery is best effort — a slow subscriber misses signals rather
// than blocking others, because every consumer keeps a polling backstop.
// Subscriber channels are never closed: after Close (or a dropped connection)
// they simply go silent, which degrades consumers to their poll cadence
// instead of spinning them.
type Listener struct {
	dsn string

	mu         sync.Mutex
	conn       *pgx.Conn
	subs       map[string]map[int64]chan string
	nextSub    int64
	listen     []string // channels waiting for a LISTEN statement
	unlisten   []string // channels waiting for an UNLISTEN statement
	waitCancel context.CancelFunc
	closed     bool

	// loopCtx drives the dispatch loop: it is canceled by both the caller's
	// context and Close, so a blocked WaitForNotification always unwinds
	// during shutdown instead of stranding the goroutine on a live socket.
	loopCtx    context.Context
	loopCancel context.CancelFunc
	done       chan struct{}
	stopped    chan struct{}
}

// subBuffer bounds per-subscriber queueing. Overflow drops the signal: a
// dropped wake costs one poll interval, never correctness.
const subBuffer = 16

// Listen opens the dedicated connection synchronously (startup fails fast on
// an unreachable database, matching Open) and starts the dispatch loop. The
// loop reconnects with backoff on connection loss, so Listen itself is the
// only failure callers must handle.
func Listen(ctx context.Context, dsn string) (*Listener, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("database listen: %w", err)
	}
	loopCtx, loopCancel := context.WithCancel(ctx)
	l := &Listener{
		dsn:        dsn,
		conn:       conn,
		subs:       map[string]map[int64]chan string{},
		loopCtx:    loopCtx,
		loopCancel: loopCancel,
		done:       make(chan struct{}),
		stopped:    make(chan struct{}),
	}
	go l.run()
	return l, nil
}

// Subscribe registers a receiver for one channel, issuing the LISTEN
// statement on the shared connection. The returned cancel function removes
// the receiver; the channel is left open for the caller to drop.
func (l *Listener) Subscribe(channel string) (<-chan string, func()) {
	if !channelPattern.MatchString(channel) {
		panic(fmt.Sprintf("database: invalid LISTEN channel %q", channel))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		// Shutdown leaves subscribers with a silent channel (never closed,
		// never written) so consumers fall back to polling instead of
		// spinning on a closed-channel receive.
		return make(chan string), func() {}
	}
	id := l.nextSub
	l.nextSub++
	ch := make(chan string, subBuffer)
	if l.subs[channel] == nil {
		l.subs[channel] = map[int64]chan string{}
		l.listen = append(l.listen, channel)
	}
	l.subs[channel][id] = ch
	l.interrupt()
	return ch, func() { l.unsubscribe(channel, id) }
}

// unsubscribe removes one receiver and, for the last receiver of a channel,
// queues the UNLISTEN statement.
func (l *Listener) unsubscribe(channel string, id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	subs := l.subs[channel]
	if subs == nil {
		return
	}
	delete(subs, id)
	if len(subs) == 0 {
		delete(l.subs, channel)
		// A re-queued LISTEN for the same channel cancels the pending
		// UNLISTEN and vice versa; drain both lists before queueing.
		for i, pending := range l.listen {
			if pending == channel {
				l.listen = append(l.listen[:i], l.listen[i+1:]...)
				break
			}
		}
		l.unlisten = append(l.unlisten, channel)
		l.interrupt()
	}
}

// interrupt wakes the dispatch loop so pending control statements run now.
func (l *Listener) interrupt() {
	if l.waitCancel != nil {
		l.waitCancel()
	}
}

// dispatch fans one notification out to the current receivers, dropping the
// signal for any receiver whose buffer is full.
func (l *Listener) dispatch(n *pgconn.Notification) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ch := range l.subs[n.Channel] {
		select {
		case ch <- n.Payload:
		default:
		}
	}
}

// Close stops the dispatch loop and releases the connection. Receivers go
// silent rather than closed; consumers' poll backstops take over.
func (l *Listener) Close() error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		close(l.done)
	}
	l.mu.Unlock()
	// Cancel the loop context so a blocked WaitForNotification unwinds;
	// safe to call more than once.
	l.loopCancel()
	<-l.stopped
	return nil
}

// run owns the connection for the process lifetime: it applies LISTEN/
// UNLISTEN control statements, waits for notifications, and rebuilds the
// connection after failures.
func (l *Listener) run() {
	defer close(l.stopped)
	// Reconnect backoff starts small (a brief failover) and grows to a
	// ceiling; any delivered notification proves health and resets it.
	backoff := 200 * time.Millisecond
	const maxBackoff = 5 * time.Second
	for {
		err := l.serve()
		l.closeConn()
		if l.loopCtx.Err() != nil || l.isClosed() {
			return
		}
		slog.Warn("database.listen_disconnected", "error_kind", errKind(err), "retry_in", backoff.String())
		select {
		case <-l.loopCtx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
		if e := l.reconnect(); e != nil {
			slog.Warn("database.listen_reconnect_failed", "error_kind", errKind(e))
		}
	}
}

// closeConn releases the current connection outside the dispatch loop so a
// dead socket never leaks across reconnects.
func (l *Listener) closeConn() {
	l.mu.Lock()
	conn := l.conn
	l.conn = nil
	l.mu.Unlock()
	if conn != nil {
		// Fresh context with a short budget: the loop context may already be
		// canceled during shutdown, but the socket still needs closing.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = conn.Close(ctx)
	}
}

// serve drives one live connection until it fails or the listener closes.
func (l *Listener) serve() error {
	for {
		if err := l.applyControl(); err != nil {
			return err
		}
		waitCtx, cancel := context.WithCancel(l.loopCtx)
		l.mu.Lock()
		l.waitCancel = cancel
		// A Subscribe/Unsubscribe may have queued control statements between
		// the applyControl drain above and this registration; its interrupt
		// call found no waitCancel to cancel. Re-check under the lock and
		// self-interrupt so the pending LISTEN/UNLISTEN still applies.
		pending := len(l.listen) > 0 || len(l.unlisten) > 0
		l.mu.Unlock()
		if pending {
			cancel()
		}
		n, err := l.connWait(waitCtx)
		cancel()
		l.mu.Lock()
		l.waitCancel = nil
		l.mu.Unlock()
		if err == nil {
			l.dispatch(n)
			continue
		}
		if l.loopCtx.Err() != nil || l.isClosed() {
			return l.loopCtx.Err()
		}
		// A canceled wait that is not a shutdown was a control interrupt:
		// keep the connection and apply the pending statements.
		if errors.Is(err, context.Canceled) {
			continue
		}
		return err
	}
}

// connWait reads one notification under the connection lock so control
// statements never race the wait.
func (l *Listener) connWait(ctx context.Context) (*pgconn.Notification, error) {
	l.mu.Lock()
	conn := l.conn
	l.mu.Unlock()
	if conn == nil {
		return nil, context.Canceled
	}
	return conn.WaitForNotification(ctx)
}

// applyControl drains the pending LISTEN/UNLISTEN queue on the current
// connection.
func (l *Listener) applyControl() error {
	for {
		l.mu.Lock()
		conn := l.conn
		listen := l.listen
		unlisten := l.unlisten
		l.listen, l.unlisten = nil, nil
		l.mu.Unlock()
		if conn == nil || (len(listen) == 0 && len(unlisten) == 0) {
			return nil
		}
		for _, channel := range unlisten {
			if _, err := conn.Exec(l.loopCtx, "UNLISTEN "+channel); err != nil {
				return err
			}
		}
		for _, channel := range listen {
			if _, err := conn.Exec(l.loopCtx, "LISTEN "+channel); err != nil {
				return err
			}
		}
	}
}

// reconnect replaces a dead connection and restores every active channel's
// LISTEN so the signaling gap spans only the reconnect window. Signals
// emitted during that gap are lost by design; poll backstops recover.
func (l *Listener) reconnect() error {
	conn, err := pgx.Connect(l.loopCtx, l.dsn)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.conn = conn
	l.listen = nil
	for channel := range l.subs {
		l.listen = append(l.listen, channel)
	}
	l.mu.Unlock()
	return nil
}

func (l *Listener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// errKind collapses driver errors for logs without leaking connection
// strings inside error texts.
func errKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var net interface{ Timeout() bool }
	if errors.As(err, &net) && net.Timeout() {
		return "timeout"
	}
	return "connection"
}
