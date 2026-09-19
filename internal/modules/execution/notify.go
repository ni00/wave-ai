package execution

import (
	"context"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/database"
)

// notifyQueued publishes the worker wake signal on an open transaction that
// just made a task claimable (creation, requeue, resume or wake). It must run
// inside the same transaction as the state change: PostgreSQL discards the
// notification if that transaction aborts, so workers never chase work that
// never committed.
func notifyQueued(tx *gorm.DB) error {
	return database.NotifyQueued(tx)
}

// EventSignaler wakes open event streams. Subscribe returns a channel that
// receives a signal whenever the session may have new committed events; it is
// canceled with ctx and never closed by the producer, so a dead signaler
// degrades the stream to its poll cadence instead of spinning it.
type EventSignaler interface {
	Subscribe(ctx context.Context, sessionID string) <-chan struct{}
}

// listenerSignaler adapts the platform listener to per-stream subscriptions:
// each stream filters the shared session-event channel for its own session.
type listenerSignaler struct {
	listener *database.Listener
}

// Signaler adapts a platform listener for the SSE handlers. A nil listener
// yields a nil EventSignaler, which streams treat as polling-only mode.
func Signaler(listener *database.Listener) EventSignaler {
	if listener == nil {
		return nil
	}
	return listenerSignaler{listener: listener}
}

func (s listenerSignaler) Subscribe(ctx context.Context, sessionID string) <-chan struct{} {
	payloads, unsubscribe := s.listener.Subscribe(database.ChannelSessionEvent)
	out := make(chan struct{}, 1)
	go func() {
		// Release the listener subscription with the stream so the filter
		// goroutine and its channel are collected with the request.
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case payload := <-payloads:
				if payload != sessionID {
					continue
				}
				select {
				case out <- struct{}{}:
				default:
					// A pending signal already covers this wake; the next
					// poll fetches every committed event regardless.
				}
			}
		}
	}()
	return out
}

// TaskQueueWake adapts the platform listener into a worker wake signal. The
// returned channel coalesces bursts of queue notifications into one wake per
// claim attempt and stays open (silent) after ctx ends, so a worker that
// outlives its signaler simply returns to polling.
func TaskQueueWake(ctx context.Context, listener *database.Listener) <-chan struct{} {
	if listener == nil {
		return nil
	}
	payloads, unsubscribe := listener.Subscribe(database.ChannelTaskQueued)
	out := make(chan struct{}, 1)
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case <-payloads:
				select {
				case out <- struct{}{}:
				default:
					// Coalesce: one pending wake already promises a claim
					// attempt that observes every committed task.
				}
			}
		}
	}()
	return out
}
