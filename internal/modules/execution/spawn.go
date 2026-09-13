package execution

import (
	"context"

	"gorm.io/gorm"

	"wave-ai.local/wave/internal/adapters/modelclient"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func Spawn(ctx context.Context, db *gorm.DB, p *auth.Principal, parentID, agentID, text string, limits ...Budget) (Task, error) {
	var child Task
	err := withTask(ctx, db, parentID, func(tx *gorm.DB, s *Session, parent *Task) error {
		if e := authorize(s, p); e != nil {
			return e
		}
		if parent.ParentID != "" || Terminal(parent.State) || parent.CancelRequested || parent.Finalizing {
			return apierr.New(409, apierr.InvalidRequest, "parent cannot spawn")
		}
		if parent.AgentCount >= parent.Budget.MaxAgents {
			return apierr.New(409, apierr.InvalidRequest, "agent budget exhausted")
		}
		a, ok := parent.Experts[agentID]
		if !ok {
			return apierr.Invalid("agent is not in the task's pinned expert roster")
		}
		if text == "" {
			return apierr.Invalid("input required")
		}
		var e error

		budget := parent.Budget.Narrow(Budget{})
		if len(limits) > 0 {
			budget = budget.Narrow(limits[0])
		}
		cfg := agents.Restrict(parent.Snapshot, a.Config)
		child = Task{
			ID:           xid.New("task"),
			SessionID:    s.ID,
			RootID:       parent.ID,
			ParentID:     parent.ID,
			AgentID:      a.ID,
			AgentVersion: a.Version,
			Snapshot:     cfg,
			State:        "queued",
			Budget:       budget,
			UsageKnown:   true,
			Messages:     []modelclient.Message{{Role: "system", Content: cfg.Instructions + "\nCoordinator task ID: " + parent.ID}},
		}
		manifest, e := resourceManifest(ctx, tx, p, *s, cfg)
		if e != nil {
			return e
		}
		if manifest != "" {
			child.Messages = append(child.Messages, modelclient.Message{Role: "user", Content: manifest})
		}
		if e = tx.Create(&child).Error; e != nil {
			return e
		}
		if e = addInput(tx, s, &child, text, "delegation", parent.ID); e != nil {
			return e
		}
		parent.AgentCount++
		if e = tx.Save(parent).Error; e != nil {
			return e
		}
		return emit(tx, s, parent.ID, "agent.created", map[string]any{"task_id": child.ID})
	})
	return child, err
}
