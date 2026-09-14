package execution

import (
	"gorm.io/gorm"
	"wave-ai.local/wave/internal/platform/telemetry"
)

// Persist measurements with the authoritative completion, never with a log that
// can precede a failed commit. Retried provider calls each have their own sample.
func saveGeneration(tx *gorm.DB, s *Session, g *Generation) error {
	if err := tx.Save(g).Error; err != nil {
		return err
	}
	if g.FinishedAt == nil {
		return nil
	}
	values := map[string]float64{"model.calls": 1}
	if g.State != "completed" {
		values["model.failed"] = 1
	}
	if g.DurationMS != nil {
		values["model.duration"] = float64(*g.DurationMS)
	}
	if g.FirstDeltaMS != nil {
		values["model.first_token"] = float64(*g.FirstDeltaMS)
	}
	if g.Usage.PromptTokens == nil || g.Usage.CompletionTokens == nil {
		values["model.usage_unknown"] = 1
	}
	if g.Usage.PromptTokens != nil {
		values["tokens.input"] = float64(*g.Usage.PromptTokens)
	}
	if g.Usage.CompletionTokens != nil {
		values["tokens.output"] = float64(*g.Usage.CompletionTokens)
	}
	if g.Usage.CacheReadTokens != nil {
		values["tokens.cached"] = float64(*g.Usage.CacheReadTokens)
	}
	return telemetry.Enqueue(tx, s.OrgID, s.OwnerID, *g.FinishedAt, values)
}
