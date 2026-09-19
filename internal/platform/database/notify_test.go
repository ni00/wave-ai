package database

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"
)

// openTestDB mirrors the module test setup: isolated PostgreSQL, skipped when
// unavailable. The publisher side runs through the gorm transaction helper so
// the tests exercise the exact production path. Package tests run
// concurrently against one database, so every assertion filters notifications
// by a unique payload instead of assuming channel silence.
func openTestDB(t *testing.T) (*gorm.DB, *Listener) {
	t.Helper()
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	db, e := Open(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	pool, _ := db.DB()
	t.Cleanup(func() { pool.Close() })
	listener, e := Listen(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	return db, listener
}

// receiveMatching waits for a notification whose payload equals want,
// ignoring foreign traffic from concurrently running test packages.
func receiveMatching(t *testing.T, ch <-chan string, want string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case payload := <-ch:
			if payload == want {
				return
			}
		case <-deadline:
			t.Fatalf("notification %q not delivered within %v", want, within)
		}
	}
}

// receiveAny waits for any notification on the channel.
func receiveAny(t *testing.T, ch <-chan string, within time.Duration) string {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(within):
		t.Fatal("notification not delivered")
		return ""
	}
}

func TestNotifySignalsCommitAndRollback(t *testing.T) {
	db, listener := openTestDB(t)
	queued, cancelQueued := listener.Subscribe(ChannelTaskQueued)
	defer cancelQueued()
	session, cancelSession := listener.Subscribe(ChannelSessionEvent)
	defer cancelSession()
	const committed = "commit-probe"
	const aborted = "abort-probe"

	// Committed transaction: both channels deliver, the session channel with
	// the session payload.
	e := db.Transaction(func(tx *gorm.DB) error {
		if e := NotifyQueued(tx); e != nil {
			return e
		}
		return NotifySessionEvent(tx, committed)
	})
	if e != nil {
		t.Fatal(e)
	}
	receiveAny(t, queued, 3*time.Second)
	receiveMatching(t, session, committed, 3*time.Second)

	// Rolled-back transaction: PostgreSQL discards the notification, so no
	// wake carrying the abort marker may arrive for work that never
	// committed (foreign traffic carries other payloads and is ignored).
	e = db.Transaction(func(tx *gorm.DB) error {
		if e := NotifySessionEvent(tx, aborted); e != nil {
			return e
		}
		return os.ErrDeadlineExceeded // abort on purpose
	})
	if e == nil {
		t.Fatal("rollback injection failed")
	}
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case payload := <-session:
			if payload == aborted {
				t.Fatal("notification delivered for an aborted transaction")
			}
		case <-deadline:
			return
		}
	}
}

func TestListenerFanOutAndUnsubscribe(t *testing.T) {
	db, listener := openTestDB(t)
	first, cancelFirst := listener.Subscribe(ChannelSessionEvent)
	second, cancelSecond := listener.Subscribe(ChannelSessionEvent)

	const fanOut = "fan-out-probe"
	e := db.Transaction(func(tx *gorm.DB) error { return NotifySessionEvent(tx, fanOut) })
	if e != nil {
		t.Fatal(e)
	}
	receiveMatching(t, first, fanOut, 3*time.Second)
	receiveMatching(t, second, fanOut, 3*time.Second)

	cancelFirst()
	// The UNLISTEN control statement must apply before the next publish; the
	// listener loop processes it on the interrupt wake.
	time.Sleep(100 * time.Millisecond)
	const afterUnsub = "after-unsub-probe"
	e = db.Transaction(func(tx *gorm.DB) error { return NotifySessionEvent(tx, afterUnsub) })
	if e != nil {
		t.Fatal(e)
	}
	receiveMatching(t, second, afterUnsub, 3*time.Second)
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case payload := <-first:
			if payload == afterUnsub {
				t.Fatal("unsubscribed receiver still delivered")
			}
		case <-deadline:
			cancelSecond()
			return
		}
	}
}

func TestListenerCloseSilencesReceivers(t *testing.T) {
	db, listener := openTestDB(t)
	queued, cancel := listener.Subscribe(ChannelTaskQueued)
	defer cancel()
	if e := db.Transaction(func(tx *gorm.DB) error { return NotifyQueued(tx) }); e != nil {
		t.Fatal(e)
	}
	receiveAny(t, queued, 3*time.Second)
	if e := listener.Close(); e != nil {
		t.Fatal(e)
	}
	// Closed receivers are silent but never closed: a receive must block
	// (poll backstops take over) instead of returning endlessly.
	select {
	case payload, ok := <-queued:
		t.Fatalf("closed listener delivered %q (open=%v)", payload, ok)
	case <-time.After(200 * time.Millisecond):
	}
}
