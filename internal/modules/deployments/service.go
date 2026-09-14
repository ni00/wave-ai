package deployments

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"wave-ai.local/wave/internal/platform/observe"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/webhooks"
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
		if scheduled && (d.Paused || d.NextAt == nil || d.NextAt.After(now)) {
			return nil
		}
		out = Run{ID: xid.New("run"), DeploymentID: id, Status: "launched", Trigger: "manual", DeploymentVersion: d.Version, AgentVersion: d.AgentVersion}
		if scheduled {
			out.Trigger = "schedule"
			out.ScheduledAt = d.NextAt
			n, e := nextInZone(d.Cron, d.Timezone, now)
			if e != nil {
				out.Status = "failed"
				out.ErrorCode = "invalid_schedule"
				out.Reason = "invalid stored schedule"
				d.Paused = true
				d.PauseReason = out.Reason
			}
			missed := now.Sub(*d.NextAt) > 30*time.Second
			d.NextAt = n
			if missed && d.MisfirePolicy != "run_once" && out.Status == "launched" {
				out.Status = "skipped"
				out.Reason = "missed schedule skipped"
			}
		}
		// A nested transaction isolates launch changes while preserving the attempt.
		if out.Status == "launched" {
			e := tx.Transaction(func(launch *gorm.DB) error {
				if d.OverlapPolicy != "allow" && d.LastTask != "" {
					var previous execution.Task
					if e := launch.Where(clause.Eq{Column: "id", Value: d.LastTask}).Take(&previous).Error; e != nil {
						return e
					}
					if !execution.Terminal(previous.State) {
						out.Status = "skipped"
						out.Reason = "previous task is still active"
						return nil
					}
				}
				if e := execution.ValidateSessionResources(ctx, launch, p, d.EnvironmentID, d.FileIDs, d.MemoryIDs, d.VaultIDs); e != nil {
					return e
				}
				s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Title: d.Name, EnvironmentID: d.EnvironmentID, FileIDs: d.FileIDs, MemoryIDs: d.MemoryIDs, VaultIDs: d.VaultIDs}
				if e := launch.Create(&s).Error; e != nil {
					return e
				}
				version := d.AgentVersion
				if d.FollowLatest {
					version = 0
				}
				task, e := execution.CreateTask(ctx, launch, p, s.ID, d.AgentID, d.Input, d.Budget, version)
				if e != nil {
					return e
				}
				out.TaskID = task.ID
				out.SessionID = s.ID
				out.AgentVersion = task.AgentVersion
				d.LastTask = task.ID
				return nil
			})
			if e != nil {
				out.Status = "failed"
				out.TaskID = ""
				out.SessionID = ""
				out.ErrorCode = "launch_failed"
				out.Reason = "task launch failed"
				var domain *apierr.Error
				if errors.As(e, &domain) && domain.Status < 500 {
					out.ErrorCode = string(domain.Type)
					out.Reason = domain.Message
					d.Paused = true
					d.PauseReason = out.Reason
				} else if errors.Is(e, gorm.ErrRecordNotFound) {
					out.ErrorCode = "resource_unavailable"
					out.Reason = "referenced resource is unavailable"
					d.Paused = true
					d.PauseReason = out.Reason
				}
			}
		}
		if e := tx.Save(&d).Error; e != nil {
			return e
		}
		if e := tx.Create(&out).Error; e != nil {
			return e
		}
		return webhooks.Enqueue(tx, p, out.ID, "deployment.run", map[string]any{"deployment_id": id, "run_id": out.ID, "task_id": out.TaskID, "status": out.Status})
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
			d.PauseReason = ""
			var e error
			d.NextAt, e = nextInZone(d.Cron, d.Timezone, time.Now())
			if e != nil {
				return e
			}
		}
		return tx.Save(&d).Error
	})
	return d, e
}
