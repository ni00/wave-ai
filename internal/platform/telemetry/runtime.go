package telemetry

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

// LogSink never blocks an API request or tool on database I/O. A failed batch
// stays pending for retry; a saturated bounded queue drops new records, counts
// them and reports the loss to process stderr. Raw process logs remain available.
type LogSink struct {
	db      *gorm.DB
	queue   chan observe.Log
	wake    chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
	Dropped atomic.Uint64
}

func NewLogSink(db *gorm.DB) *LogSink {
	ctx, cancel := context.WithCancel(context.Background())
	s := &LogSink{db: db, queue: make(chan observe.Log, 4096), wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	go s.run(ctx)
	return s
}
func (s *LogSink) Write(row observe.Log) {
	if row.OrgID == "" || row.OwnerID == "" {
		return
	}
	row.ID = xid.New("log")
	select {
	case s.queue <- row:
		if len(s.queue) >= 128 {
			select {
			case s.wake <- struct{}{}:
			default:
			}
		}
	default:
		s.Dropped.Add(1)
	}
}
func (s *LogSink) Close() { s.cancel(); <-s.done }
func (s *LogSink) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var pending []observe.Log
	var reported uint64
	flush := func() {
		for len(pending) < 128 {
			select {
			case row := <-s.queue:
				pending = append(pending, row)
			default:
				goto write
			}
		}
	write:
		if len(pending) > 0 {
			write, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := s.db.WithContext(write).Clauses(clause.OnConflict{DoNothing: true}).Create(&pending).Error
			cancel()
			if err == nil {
				pending = nil
			} else {
				slog.Warn("monitor.log_write_failed", "error_kind", observe.ErrorKind(err))
			}
		}
		if n := s.Dropped.Load(); n != reported {
			slog.Warn("monitor.logs_dropped", "count", n-reported)
			reported = n
		}
	}
	for {
		select {
		case <-ctx.Done():
			deadline := time.Now().Add(5 * time.Second)
			for len(s.queue) > 0 || len(pending) > 0 {
				flush()
				if len(pending) > 0 || time.Now().After(deadline) {
					s.Dropped.Add(uint64(len(pending) + len(s.queue)))
					return
				}
			}
			return
		case <-ticker.C:
			flush()
		case <-s.wake:
			flush()
			if len(pending) == 0 && len(s.queue) >= 128 {
				select {
				case s.wake <- struct{}{}:
				default:
				}
			}
		}
	}
}

func Run(ctx context.Context, db *gorm.DB) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for i := 0; i < 16; i++ {
				batch, cancel := context.WithTimeout(ctx, 5*time.Second)
				n, err := Reduce(batch, db)
				cancel()
				if err != nil {
					if ctx.Err() == nil {
						slog.Warn("monitor.reduce_failed", "error_kind", observe.ErrorKind(err))
					}
					break
				}
				if n < 256 {
					break
				}
			}
		case <-cleanup.C:
			clean, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := Prune(clean, db, time.Now()); err != nil && ctx.Err() == nil {
				slog.Warn("monitor.prune_failed", "error_kind", observe.ErrorKind(err))
			}
			cancel()
		}
	}
}

// Bound each retention deletion; never scan or delete execution source records.
func Prune(ctx context.Context, db *gorm.DB, now time.Time) error {
	for _, spec := range []struct {
		model     any
		condition clause.Expression
	}{
		{&observe.Log{}, clause.Lt{Column: "created_at", Value: now.Add(-7 * 24 * time.Hour)}},
		{&Bucket{}, clause.Or(clause.And(clause.Eq{Column: "seconds", Value: 60}, clause.Lt{Column: "started_at", Value: now.Add(-48 * time.Hour)}), clause.Lt{Column: "started_at", Value: now.Add(-31 * 24 * time.Hour)})},
	} {
		for {
			var rows []struct{ ID string }
			if err := db.WithContext(ctx).Model(spec.model).Select("id").Where(spec.condition).Limit(500).Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			ids := make([]any, len(rows))
			for i, r := range rows {
				ids[i] = r.ID
			}
			if err := db.WithContext(ctx).Where(clause.IN{Column: "id", Values: ids}).Delete(spec.model).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
