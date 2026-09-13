package agents

import (
	"context"
	"net/url"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func Get(ctx context.Context, db *gorm.DB, p *auth.Principal, id string) (Agent, error) {
	var a Agent
	err := auth.Owned(db.WithContext(ctx), p).Where(clause.Eq{Column: "id", Value: id}).Take(&a).Error
	return a, err
}
func Validate(c Config) error {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Model) == "" {
		return apierr.Invalid("name and model are required")
	}
	if len(c.Tools) > 128 {
		return apierr.Invalid("at most 128 tools")
	}
	seen := map[string]bool{}
	for _, t := range c.Tools {
		if t.Name == "" || strings.HasPrefix(t.Name, "agent_") || seen[t.Name] {
			return apierr.Invalid("invalid or duplicate tool name %q", t.Name)
		}
		seen[t.Name] = true
		switch t.Kind {
		case "builtin":
			switch t.Name {
			case "bash", "read", "write", "edit", "ls", "grep", "find":
			default:
				return apierr.Invalid("unknown builtin %q", t.Name)
			}
		case "mcp":
			u, e := url.Parse(t.ServerURL)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return apierr.Invalid("MCP server must be an HTTPS URL without embedded credentials")
			}
		case "custom":
			if t.Parameters == nil {
				return apierr.Invalid("custom tool parameters required")
			}
		default:
			return apierr.Invalid("unsupported tool kind")
		}
	}
	return nil
}
func Create(ctx context.Context, db *gorm.DB, p *auth.Principal, c Config) (Agent, error) {
	a := Agent{ID: xid.New("agent"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Version: 1, Config: c}
	if err := Validate(c); err != nil {
		return a, err
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&a).Error; err != nil {
			return err
		}
		return tx.Create(&Version{AgentID: a.ID, Number: 1, Config: c}).Error
	})
	return a, err
}
func Update(ctx context.Context, db *gorm.DB, p *auth.Principal, id string, expected int, c Config) (Agent, error) {
	var a Agent
	if err := Validate(c); err != nil {
		return a, err
	}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = Get(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), p, id)
		if err != nil {
			return err
		}
		if a.Version != expected {
			return apierr.New(409, apierr.InvalidRequest, "agent version conflict")
		}
		if a.Archived {
			return apierr.Invalid("agent is archived")
		}
		a.Version++
		a.Config = c
		if err = tx.Save(&a).Error; err != nil {
			return err
		}
		return tx.Create(&Version{AgentID: id, Number: a.Version, Config: c}).Error
	})
	return a, err
}

// Restrict retains only capabilities that both parent and expert authorize.
func Restrict(parent, expert Config) Config {
	allowed := map[string]Tool{}
	for _, t := range parent.Tools {
		allowed[t.Name] = t
	}
	out := expert
	out.Tools = nil
	out.SkillIDs = nil
	out.ExpertIDs = nil
	for _, id := range expert.SkillIDs {
		for _, allowed := range parent.SkillIDs {
			if id == allowed {
				out.SkillIDs = append(out.SkillIDs, id)
				break
			}
		}
	}
	for _, t := range expert.Tools {
		p, ok := allowed[t.Name]
		if ok && p.Kind == t.Kind && p.ServerURL == t.ServerURL && p.RemoteName == t.RemoteName && p.CredentialID == t.CredentialID {
			p.Approval = p.Approval || t.Approval
			out.Tools = append(out.Tools, p)
		}
	}
	return out
}
