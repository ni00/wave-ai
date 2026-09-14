package deployments

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
	_ "time/tzdata"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func nextInZone(spec, zone string, now time.Time) (*time.Time, error) {
	if zone == "" {
		zone = "UTC"
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return nil, apierr.Invalid("invalid IANA timezone")
	}
	if spec == "" {
		return nil, nil
	}
	if len(strings.Fields(spec)) != 5 {
		return nil, apierr.Invalid("cron requires five fields")
	}
	return next("CRON_TZ="+zone+" "+spec, now)
}
func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Deployment, error) {
	var d Deployment
	e := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&d).Error
	return d, e
}
func configure(ctx context.Context, db *gorm.DB, p *auth.Principal, d *Deployment, c CreateRequest) error {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Input) == "" {
		return apierr.Invalid("name and input are required")
	}
	if c.AgentVersion < 0 {
		return apierr.Invalid("agent_version cannot be negative")
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if c.MisfirePolicy == "" {
		c.MisfirePolicy = "skip"
	}
	if c.OverlapPolicy == "" {
		c.OverlapPolicy = "skip"
	}
	if c.MisfirePolicy != "skip" && c.MisfirePolicy != "run_once" {
		return apierr.Invalid("invalid misfire_policy")
	}
	if c.OverlapPolicy != "skip" && c.OverlapPolicy != "allow" {
		return apierr.Invalid("invalid overlap_policy")
	}
	next, e := nextInZone(c.Cron, c.Timezone, time.Now())
	if e != nil {
		return e
	}
	a, e := agents.GetVersion(ctx, db, p, c.AgentID, c.AgentVersion)
	if e != nil {
		return e
	}
	if a.Archived {
		return apierr.Invalid("agent is archived")
	}
	if e = execution.ValidateSessionResources(ctx, db, p, c.EnvironmentID, c.FileIDs, c.MemoryIDs, c.VaultIDs); e != nil {
		return e
	}
	d.Name = c.Name
	d.AgentID = c.AgentID
	d.AgentVersion = a.Version
	d.FollowLatest = c.FollowLatest
	d.EnvironmentID = c.EnvironmentID
	d.Input = c.Input
	d.Cron = c.Cron
	d.Timezone = c.Timezone
	d.NextAt = next
	d.Budget = c.Budget.Defaults()
	d.FileIDs = c.FileIDs
	d.MemoryIDs = c.MemoryIDs
	d.VaultIDs = c.VaultIDs
	d.MisfirePolicy = c.MisfirePolicy
	d.OverlapPolicy = c.OverlapPolicy
	return nil
}
func snapshot(d Deployment) CreateRequest {
	return CreateRequest{Name: d.Name, AgentID: d.AgentID, AgentVersion: d.AgentVersion, FollowLatest: d.FollowLatest, EnvironmentID: d.EnvironmentID, Input: d.Input, Cron: d.Cron, Timezone: d.Timezone, Budget: d.Budget, FileIDs: d.FileIDs, MemoryIDs: d.MemoryIDs, VaultIDs: d.VaultIDs, MisfirePolicy: d.MisfirePolicy, OverlapPolicy: d.OverlapPolicy}
}
func Create(ctx context.Context, db *gorm.DB, p *auth.Principal, c CreateRequest) (Deployment, error) {
	d := Deployment{ID: xid.New("deployment"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Version: 1}
	e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := configure(ctx, tx, p, &d, c); e != nil {
			return e
		}
		if e := tx.Create(&d).Error; e != nil {
			return e
		}
		return tx.Create(&Version{DeploymentID: d.ID, Number: d.Version, Config: snapshot(d)}).Error
	})
	return d, e
}
func Update(ctx context.Context, db *gorm.DB, p *auth.Principal, id string, in UpdateRequest) (Deployment, error) {
	var d Deployment
	e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		d, e = Get(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), p, id)
		if e != nil {
			return e
		}
		if d.Version != in.Version {
			return apierr.Conflict("deployment version conflict")
		}
		// Retain the previous configuration for deployments created before versioning.
		if e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Version{DeploymentID: d.ID, Number: d.Version, Config: snapshot(d)}).Error; e != nil {
			return e
		}
		if e = configure(ctx, tx, p, &d, in.Config); e != nil {
			return e
		}
		d.Version++
		if e = tx.Save(&d).Error; e != nil {
			return e
		}
		return tx.Create(&Version{DeploymentID: d.ID, Number: d.Version, Config: snapshot(d)}).Error
	})
	return d, e
}
func Upcoming(d Deployment, now time.Time) (ScheduleResponse, error) {
	out := ScheduleResponse{Times: []time.Time{}}
	for i := 0; i < 5; i++ {
		n, e := nextInZone(d.Cron, d.Timezone, now)
		if e != nil {
			return out, e
		}
		if n == nil {
			break
		}
		out.Times = append(out.Times, *n)
		now = *n
	}
	return out, nil
}
