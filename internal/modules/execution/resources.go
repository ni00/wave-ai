package execution

import (
	"context"
	"gorm.io/gorm"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/modules/vault"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
)

func ValidateSessionResources(ctx context.Context, db *gorm.DB, p *auth.Principal, envID string, fileIDs, memoryIDs, vaultIDs []string) error {
	if envID != "" {
		env, e := environments.Get(ctx, db, p, envID)
		if e != nil {
			return e
		}
		if env.Archived {
			return apierr.Invalid("environment archived")
		}
	}
	if (len(fileIDs) > 0 || len(memoryIDs) > 0) && envID == "" {
		return apierr.Invalid("mounted resources require an environment")
	}
	if len(fileIDs) > 100 || len(memoryIDs) > 20 || len(vaultIDs) > 20 {
		return apierr.Invalid("too many mounted resources")
	}
	for _, id := range fileIDs {
		if _, e := files.Get(ctx, db, p, id); e != nil {
			return e
		}
	}
	for _, id := range memoryIDs {
		if _, e := memory.Get(ctx, db, p, id); e != nil {
			return e
		}
	}
	for _, id := range vaultIDs {
		v, e := vault.Get(ctx, db, p, id)
		if e != nil {
			return e
		}
		if v.Archived {
			return apierr.Invalid("vault archived")
		}
	}
	return nil
}
