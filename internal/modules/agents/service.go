package agents

import (
	"context"
	"net/url"
	"path"
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

// GetVersion resolves an immutable configuration within the current owner's scope.
func GetVersion(ctx context.Context, db *gorm.DB, p *auth.Principal, id string, version int) (Agent, error) {
	a, err := Get(ctx, db, p, id)
	if err != nil || version == 0 || version == a.Version {
		return a, err
	}
	var v Version
	err = db.WithContext(ctx).Where(clause.And(clause.Eq{Column: "agent_id", Value: id}, clause.Eq{Column: "number", Value: version})).Take(&v).Error
	if err == nil {
		a.Version = v.Number
		a.Config = v.Config
	}
	return a, err
}

func pinExperts(ctx context.Context, db *gorm.DB, p *auth.Principal, self string, c *Config) error {
	versions := map[string]int{}
	for _, id := range c.ExpertIDs {
		if id == "" {
			return apierr.Invalid("invalid expert ID")
		}
		if _, exists := versions[id]; exists {
			return apierr.Invalid("duplicate expert ID")
		}
		a, err := GetVersion(ctx, db, p, id, c.ExpertVersions[id])
		if err != nil {
			return err
		}
		if a.Archived {
			return apierr.Invalid("expert is archived")
		}
		versions[id] = a.Version
	}
	c.ExpertVersions = versions
	return nil
}

func Validate(c Config) error {
	if len(c.Acceptance) > 20 {
		return apierr.Invalid("at most 20 acceptance checks")
	}
	for _, check := range c.Acceptance {
		if check.Kind != "file_exists" && check.Kind != "json" {
			return apierr.Invalid("invalid acceptance check kind")
		}
		if check.Kind == "file_exists" && check.Path == "" {
			return apierr.Invalid("file_exists requires a path")
		}
		if check.Path != "" && (path.IsAbs(check.Path) || path.Clean(check.Path) != check.Path || check.Path == ".." || strings.HasPrefix(check.Path, "../") || strings.ContainsAny(check.Path, "\\\x00")) {
			return apierr.Invalid("acceptance path must be relative to outputs")
		}
		if len(check.Required) > 100 || len(check.Types) > 100 {
			return apierr.Invalid("too many acceptance fields")
		}
		for _, kind := range check.Types {
			switch kind {
			case "string", "number", "boolean", "object", "array", "null":
			default:
				return apierr.Invalid("invalid JSON field type")
			}
		}
	}

	if len(c.ExpertIDs) > 16 {
		return apierr.Invalid("at most 16 configured experts")
	}
	if c.DelegationPolicy != "" && c.DelegationPolicy != "intersection" && c.DelegationPolicy != "explicit" {
		return apierr.Invalid("invalid delegation_policy")
	}
	for _, version := range c.ExpertVersions {
		if version < 0 {
			return apierr.Invalid("expert version cannot be negative")
		}
	}

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
		if err := pinExperts(ctx, tx, p, a.ID, &c); err != nil {
			return err
		}
		a.Config = c
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
		if err := pinExperts(ctx, tx, p, id, &c); err != nil {
			return err
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

// Delegated applies the coordinator's explicit delegation policy. Expert IDs are
// an authorization roster; delegated configurations cannot delegate again.
func Delegated(parent, expert Config) Config {
	if parent.DelegationPolicy != "explicit" {
		return Restrict(parent, expert)
	}
	expert.ExpertIDs = nil
	expert.ExpertVersions = nil
	return expert
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
	out.ExpertVersions = nil
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
