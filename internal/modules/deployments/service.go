package deployments

import (
	"context"
	"log/slog"
	"time"
	"wave-ai.local/wave/internal/platform/observe"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func next(spec string, now time.Time) (*time.Time, error) {
	if spec == "" {
		return nil, nil
	}
	s, e := cron.ParseStandard(spec)
	if e != nil {
		return nil, apierr.Invalid("invalid cron: %v", e)
	}
	t := s.Next(now)
	if t.IsZero() {
		return nil, apierr.Invalid("cron has no future occurrence")
	}
	return &t, nil
}
func Fire(ctx context.Context, db *gorm.DB, p *auth.Principal, id string, scheduled bool) (Run, error) {
	var out Run
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var d Deployment
		if e := auth.Owned(tx.Clauses(clause.Locking{Strength: "UPDATE"}), p).Where(clause.Eq{Column: "id", Value: id}).Take(&d).Error; e != nil {
			return e
		}
		now := time.Now()
		if d.Paused {
			return apierr.New(409, apierr.InvalidRequest, "deployment paused")
		}
		if scheduled && (d.NextAt == nil || d.NextAt.After(now)) {
			return nil
		}
		out = Run{ID: xid.New("run"), DeploymentID: id}
		if scheduled {
			n, e := next(d.Cron, now)
			if e != nil {
				return e
			}
			missed := now.Sub(*d.NextAt) > 30*time.Second
			d.NextAt = n
			if missed {
				out.Reason = "missed schedule skipped"
				if e = tx.Save(&d).Error; e != nil {
					return e
				}
				return tx.Create(&out).Error
			}
			if d.LastTask != "" {
				var previous execution.Task
				if e = tx.Where(clause.Eq{Column: "id", Value: d.LastTask}).Take(&previous).Error; e != nil {
					return e
				}
				if !execution.Terminal(previous.State) {
					out.Reason = "previous task is still active"
					if e = tx.Save(&d).Error; e != nil {
						return e
					}
					return tx.Create(&out).Error
				}
			}
		}
		if d.EnvironmentID != "" {
			env, e := environments.Get(ctx, tx, p, d.EnvironmentID)
			if e != nil {
				return e
			}
			if env.Archived {
				return apierr.Invalid("environment archived")
			}
		}
		s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Title: d.Name, EnvironmentID: d.EnvironmentID}
		if e := tx.Create(&s).Error; e != nil {
			return e
		}
		task, e := execution.CreateTask(ctx, tx, p, s.ID, d.AgentID, d.Input, execution.Budget{})
		if e != nil {
			return e
		}
		out.TaskID = task.ID
		out.SessionID = s.ID
		d.LastTask = task.ID
		if e = tx.Save(&d).Error; e != nil {
			return e
		}
		return tx.Create(&out).Error
	})
	return out, err
}
func RunScheduler(ctx context.Context, db *gorm.DB) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		rows := []Deployment{}
		if e := db.WithContext(ctx).Where(clause.And(clause.Eq{Column: "paused", Value: false}, clause.Lte{Column: "next_at", Value: time.Now()})).Limit(100).Find(&rows).Error; e != nil {
			slog.Error("scheduler query", "error_kind", observe.ErrorKind(e))
			continue
		}
		for _, d := range rows {
			_, e := Fire(ctx, db, &auth.Principal{OrgID: d.OrgID, PrincipalID: d.OwnerID}, d.ID, true)
			if e != nil {
				slog.Warn("scheduled run", "deployment", d.ID, "error_kind", observe.ErrorKind(e))
			}
		}
	}
}

func Pause(ctx context.Context, db *gorm.DB, p *auth.Principal, id string, paused bool) (Deployment, error) {
	var d Deployment
	e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := auth.Owned(tx.Clauses(clause.Locking{Strength: "UPDATE"}), p).Where(clause.Eq{Column: "id", Value: id}).Take(&d).Error; e != nil {
			return e
		}
		if d.Paused == paused {
			return nil
		}
		d.Paused = paused
		if !paused {
			var e error
			d.NextAt, e = next(d.Cron, time.Now())
			if e != nil {
				return e
			}
		}
		return tx.Save(&d).Error
	})
	return d, e
}
