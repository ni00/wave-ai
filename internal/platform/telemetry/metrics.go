// Package telemetry persists structured logs and incrementally reduced metrics.
package telemetry

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/observe"
	"wave-ai.local/wave/internal/platform/xid"
)

// Samples are a transactional outbox. They are removed in the same transaction
// that updates both rollups, so restarts/competing reducers cannot double count.
type Sample struct {
	ID        string             `gorm:"primaryKey"`
	OrgID     string             `gorm:"index:sample_owner,priority:1"`
	OwnerID   string             `gorm:"index:sample_owner,priority:2"`
	CreatedAt time.Time          `gorm:"index:sample_owner,priority:3"`
	Values    map[string]float64 `gorm:"serializer:json;type:jsonb"`
}

func (Sample) TableName() string { return "monitor_samples" }

type Bucket struct {
	ID        string                  `gorm:"primaryKey"`
	OrgID     string                  `gorm:"index:metric_owner_time,priority:1"`
	OwnerID   string                  `gorm:"index:metric_owner_time,priority:2"`
	Seconds   int                     `gorm:"index:metric_owner_time,priority:3"`
	StartedAt time.Time               `gorm:"index:metric_owner_time,priority:4;index"`
	Values    map[string]Distribution `gorm:"serializer:json;type:jsonb"`
}

func (Bucket) TableName() string { return "monitor_buckets" }
func Models() []any              { return []any{&observe.Log{}, &Sample{}, &Bucket{}} }

// Names are fixed; task/session IDs and user-supplied labels are never series keys.
var Units = map[string]string{
	"tasks.completed": "count", "tasks.succeeded": "count", "tasks.failed": "count", "tasks.canceled": "count", "tasks.partial": "count",
	"task.duration": "ms", "queue.wait": "ms", "model.first_token": "ms", "model.duration": "ms", "sandbox.startup": "ms",
	"model.calls": "count", "model.failed": "count", "model.usage_unknown": "count", "tokens.input": "tokens", "tokens.output": "tokens", "tokens.cached": "tokens", "sandbox.failed": "count",
}

func Enqueue(tx *gorm.DB, org, owner string, at time.Time, values map[string]float64) error {
	clean := map[string]float64{}
	for k, v := range values {
		if _, ok := Units[k]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
			clean[k] = v
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return tx.Create(&Sample{ID: xid.New("sample"), OrgID: org, OwnerID: owner, CreatedAt: at.UTC(), Values: clean}).Error
}

// Positive values use a fixed logarithmic histogram (2% bin width). Zero has
// its own bin. Histograms merge, unlike percentiles; returned values are bounded
// by observed min/max, and percentile estimates have <=2% relative bin error.
type Distribution struct {
	Count int64         `json:"count"`
	Sum   float64       `json:"sum"`
	Min   float64       `json:"min"`
	Max   float64       `json:"max"`
	Bins  map[int]int64 `json:"bins,omitempty"`
}

func (d *Distribution) Add(v float64, histogram bool) {
	if d.Count == 0 || v < d.Min {
		d.Min = v
	}
	if d.Count == 0 || v > d.Max {
		d.Max = v
	}
	d.Count++
	d.Sum += v
	if histogram {
		if d.Bins == nil {
			d.Bins = map[int]int64{}
		}
		bin := -100000
		if v > 0 {
			bin = int(math.Ceil(math.Log(v) / math.Log(1.02)))
		}
		d.Bins[bin]++
	}
}
func (d *Distribution) Merge(o Distribution) {
	if o.Count == 0 {
		return
	}
	if d.Count == 0 || o.Min < d.Min {
		d.Min = o.Min
	}
	if d.Count == 0 || o.Max > d.Max {
		d.Max = o.Max
	}
	d.Count += o.Count
	d.Sum += o.Sum
	if len(o.Bins) > 0 {
		if d.Bins == nil {
			d.Bins = map[int]int64{}
		}
		for k, v := range o.Bins {
			d.Bins[k] += v
		}
	}
}
func (d Distribution) Quantile(q float64) *float64 {
	if d.Count == 0 || len(d.Bins) == 0 {
		return nil
	}
	keys := make([]int, 0, len(d.Bins))
	for k := range d.Bins {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	rank := int64(math.Ceil(float64(d.Count) * q))
	var n int64
	for _, k := range keys {
		n += d.Bins[k]
		if n >= rank {
			v := 0.0
			if k != -100000 {
				v = math.Pow(1.02, float64(k)-0.5)
			}
			v = max(d.Min, min(d.Max, v))
			return &v
		}
	}
	return nil
}

func Reduce(ctx context.Context, db *gorm.DB) (int, error) {
	count := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var samples []Sample
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Limit(256).Find(&samples).Error; e != nil {
			return e
		}
		count = len(samples)
		if count == 0 {
			return nil
		}
		grouped := map[string]*Bucket{}
		ids := make([]any, 0, count)
		for _, s := range samples {
			ids = append(ids, s.ID)
			for _, seconds := range []int{60, 3600} {
				at := s.CreatedAt.UTC().Truncate(time.Duration(seconds) * time.Second)
				id := fmt.Sprintf("%s/%s/%d/%d", s.OrgID, s.OwnerID, seconds, at.Unix())
				b := grouped[id]
				if b == nil {
					b = &Bucket{ID: id, OrgID: s.OrgID, OwnerID: s.OwnerID, Seconds: seconds, StartedAt: at, Values: map[string]Distribution{}}
					grouped[id] = b
				}
				for name, v := range s.Values {
					d := b.Values[name]
					d.Add(v, Units[name] == "ms")
					b.Values[name] = d
				}
			}
		}
		keys := make([]string, 0, len(grouped))
		for k := range grouped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, id := range keys {
			incoming := grouped[id]
			empty := *incoming
			empty.Values = map[string]Distribution{}
			if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&empty).Error; e != nil {
				return e
			}
			var current Bucket
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(clause.Eq{Column: "id", Value: id}).Take(&current).Error; e != nil {
				return e
			}
			if current.Values == nil {
				current.Values = map[string]Distribution{}
			}
			for name, d := range incoming.Values {
				existing := current.Values[name]
				existing.Merge(d)
				current.Values[name] = existing
			}
			if e := tx.Save(&current).Error; e != nil {
				return e
			}
		}
		return tx.Where(clause.IN{Column: "id", Values: ids}).Delete(&Sample{}).Error
	})
	return count, err
}

type Value struct {
	Count int64    `json:"count" validate:"required" format:"int64"`
	Sum   float64  `json:"sum" validate:"required"`
	P50   *float64 `json:"p50" validate:"required" extensions:"x-nullable"`
	P95   *float64 `json:"p95" validate:"required" extensions:"x-nullable"`
	P99   *float64 `json:"p99" validate:"required" extensions:"x-nullable"`
}
type Point struct {
	At     time.Time        `json:"at" validate:"required" format:"date-time"`
	Values map[string]Value `json:"values" validate:"required"`
}
type Metrics struct {
	From         time.Time        `json:"from" validate:"required" format:"date-time"`
	To           time.Time        `json:"to" validate:"required" format:"date-time"`
	StepSeconds  int              `json:"step_seconds" validate:"required"`
	Points       []Point          `json:"points" validate:"required"`
	Totals       map[string]Value `json:"totals" validate:"required"`
	PendingSince *time.Time       `json:"pending_since" validate:"required" extensions:"x-nullable" format:"date-time"`
}

func summarize(values map[string]Distribution) map[string]Value {
	out := map[string]Value{}
	for k, d := range values {
		out[k] = Value{Count: d.Count, Sum: d.Sum, P50: d.Quantile(.5), P95: d.Quantile(.95), P99: d.Quantile(.99)}
	}
	return out
}

// Query reads only rollups. Time ranges are aligned outwards and returned
// explicitly, so point-to-trace links use exactly the measured interval.
func Query(ctx context.Context, db *gorm.DB, p *auth.Principal, from, to time.Time) (Metrics, error) {
	if !from.Before(to) || to.Sub(from) > 30*24*time.Hour {
		return Metrics{}, apierr.Invalid("invalid metrics range")
	}
	resolution, step := 60, 60
	if to.Sub(from) > 6*time.Hour {
		step = 300
	}
	if to.Sub(from) > 24*time.Hour || from.Before(time.Now().Add(-48*time.Hour)) {
		resolution, step = 3600, 3600
	}
	width := time.Duration(step) * time.Second
	from = from.UTC().Truncate(width)
	to = to.UTC()
	if !to.Equal(to.Truncate(width)) {
		to = to.Truncate(width).Add(width)
	}
	out := Metrics{From: from, To: to, StepSeconds: step, Points: []Point{}, Totals: map[string]Value{}}
	var rows []Bucket
	err := auth.Owned(db.WithContext(ctx), p).Where(clause.And(clause.Eq{Column: "seconds", Value: resolution}, clause.Gte{Column: "started_at", Value: from}, clause.Lt{Column: "started_at", Value: to})).Order(clause.OrderByColumn{Column: clause.Column{Name: "started_at"}}).Limit(1442).Find(&rows).Error
	if err != nil {
		return out, err
	}
	totals := map[string]Distribution{}
	buckets := map[int64]map[string]Distribution{}
	for _, b := range rows {
		key := b.StartedAt.Truncate(width).Unix()
		values := buckets[key]
		if values == nil {
			values = map[string]Distribution{}
			buckets[key] = values
		}
		for name, d := range b.Values {
			v := values[name]
			v.Merge(d)
			values[name] = v
			v = totals[name]
			v.Merge(d)
			totals[name] = v
		}
	}
	for at := from; at.Before(to); at = at.Add(width) {
		out.Points = append(out.Points, Point{At: at, Values: summarize(buckets[at.Unix()])})
	}
	out.Totals = summarize(totals)
	var pending []Sample
	if err = auth.Owned(db.WithContext(ctx), p).Select("created_at").Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).Limit(1).Find(&pending).Error; err == nil && len(pending) > 0 {
		out.PendingSince = &pending[0].CreatedAt
	}
	return out, err
}
