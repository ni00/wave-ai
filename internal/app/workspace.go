package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/modules/skills"
	"wave-ai.local/wave/internal/platform/blobstore"
)

type Workspace = execution.Workspace

func (a *App) prepare(ctx context.Context, s execution.Session, t execution.Task) error {
	if s.EnvironmentID == "" {
		return nil
	}
	ready := false
	var ws Workspace
	e := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked execution.Session
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(clause.Eq{Column: "id", Value: s.ID}).Take(&locked).Error; e != nil {
			return e
		}
		if e := tx.Where(clause.Eq{Column: "session_id", Value: s.ID}).Find(&ws).Error; e != nil {
			return e
		}
		if ws.RootID == t.RootID && ws.State == "ready" {
			ready = true
			return nil
		}
		if t.ParentID != "" {
			return execution.ErrPending
		}
		if ws.RootID == t.RootID && ws.State == "preparing" {
			return errors.New("workspace preparation outcome unknown; reconcile before retry")
		}
		ws.SessionID, ws.RootID, ws.State = s.ID, t.RootID, "preparing"
		return tx.Save(&ws).Error
	})
	if e != nil || ready {
		return e
	}
	if e = a.Sandbox.Ensure(ctx, s.ID); e != nil {
		return e
	}
	env, e := environments.Get(ctx, a.DB, principal(s), s.EnvironmentID)
	if e != nil {
		return e
	}
	identity, e := a.Sandbox.Identity(ctx, s.ID)
	if e != nil {
		return e
	}
	spec, _ := json.Marshal(env.Packages)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(append([]byte(identity+"\x00"), spec...)))
	managers := make([]string, 0, len(env.Packages))
	for manager := range env.Packages {
		managers = append(managers, manager)
	}
	sort.Strings(managers)
	if ws.PackageFingerprint != fingerprint {
		for _, manager := range managers {
			pkgs := env.Packages[manager]
			if len(pkgs) == 0 {
				continue
			}
			args := []string{}
			for _, p := range pkgs {
				if strings.HasPrefix(p, "-") {
					return errors.New("package names cannot be options")
				}
				args = append(args, quote(p))
			}
			prefix := map[string]string{
				"apt":   "apt-get update && apt-get install -y -- ",
				"npm":   "npm install -- ",
				"pip":   "python3 -m pip install -- ",
				"go":    "go install ",
				"cargo": "cargo install -- ",
				"gem":   "gem install -- ",
			}[manager]
			if prefix == "" {
				return errors.New("unsupported package manager")
			}
			result, e := a.Sandbox.Exec(ctx, s.ID, sandbox.ExecRequest{Command: prefix + strings.Join(args, " "), RequestID: t.RootID + "-packages-" + manager, TimeoutSec: 600})
			if e != nil {
				return e
			}
			if result.ExitCode != 0 {
				return fmt.Errorf("install %s failed: %s", manager, result.Stderr)
			}
		}
		ws.PackageFingerprint = fingerprint
		if e = a.DB.WithContext(ctx).Model(&Workspace{}).Where(clause.Eq{Column: "session_id", Value: s.ID}).Update("package_fingerprint", fingerprint).Error; e != nil {
			return e
		}
	}

	for _, id := range s.FileIDs {
		f, e := files.GetPinned(ctx, a.DB, principal(s), id)
		if e != nil {
			return e
		}
		b, e := a.Blobs.ReadAll(ctx, blobstore.Scope{OrgID: s.OrgID, OwnerID: s.OwnerID}, f.BlobKey, 32<<20)
		if e != nil {
			return e
		}
		if e = a.Sandbox.StageInput(ctx, s.ID, id+"/"+f.Name, b); e != nil {
			return e
		}
	}
	skillIDs := append([]string{}, t.Snapshot.SkillIDs...)
	for _, expert := range t.Experts {
		skillIDs = append(skillIDs, agents.Restrict(t.Snapshot, expert.Config).SkillIDs...)
	}
	seenSkills := map[string]bool{}
	for _, id := range skillIDs {
		if seenSkills[id] {
			continue
		}
		seenSkills[id] = true
		_, items, e := skills.Load(ctx, a.DB, a.Blobs, principal(s), id)
		if e != nil {
			return e
		}
		if e = a.Sandbox.StageSkill(ctx, s.ID, id, items); e != nil {
			return e
		}
	}
	for _, id := range s.MemoryIDs {
		mount := "/mnt/memory/" + id
		res, e := a.Sandbox.Exec(ctx, s.ID, sandbox.ExecRequest{Command: "rm -rf -- " + quote(mount) + " && mkdir -p -- " + quote(mount), RequestID: t.RootID + "-memory-" + id, TimeoutSec: 30})
		if e != nil {
			return e
		}
		if res.ExitCode != 0 {
			return errors.New("memory mount preparation failed")
		}
		rows, e := memory.Project(ctx, a.DB, principal(s), s.ID, id)
		if e != nil {
			return e
		}
		for _, row := range rows {
			if row.Deleted {
				continue
			}
			if e = a.Sandbox.StageMemory(ctx, s.ID, "/mnt/memory/"+id, row.Path, []byte(row.Content), true); e != nil {
				return e
			}

		}
	}
	ws.State = "ready"
	return a.DB.WithContext(ctx).Save(&ws).Error
}
func (a *App) finish(ctx context.Context, s execution.Session, t execution.Task) error {
	if s.EnvironmentID == "" {
		return nil
	}
	if s.WriterCall != "" {
		return errors.New("workspace still has an unresolved writer")
	}
	e := a.Sandbox.VisitOutputs(ctx, s.ID, 32<<20, func(name string, data []byte) error {
		rel := strings.TrimPrefix(name, "/mnt/session/outputs/")
		if rel == name || path.IsAbs(rel) || path.Clean(rel) != rel || strings.HasPrefix(rel, "../") {
			return errors.New("invalid artifact path")
		}
		_, err := files.PutArtifact(ctx, a.DB, a.Blobs, principal(s), s.ID, t.ID, rel, data)
		return err
	})
	if e != nil {
		return e
	}

	for _, id := range s.MemoryIDs {
		local, e := a.memoryFiles(ctx, s.ID, "/mnt/memory/"+id)
		if e != nil {
			return e
		}
		if e = memory.WriteBack(ctx, a.DB, principal(s), s.ID, id, local); e != nil {
			return e
		}
	}

	return a.Sandbox.Stop(ctx, s.ID)
}

func (a *App) memoryFiles(ctx context.Context, sid, root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	total := 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if depth > 20 {
			return errors.New("memory directory depth exceeded")
		}
		names, e := a.Sandbox.ListDir(ctx, sid, dir)
		if e != nil {
			return e
		}
		for _, name := range names {
			isDir := strings.HasSuffix(name, "/")
			name = strings.TrimSuffix(name, "/")
			if name == "" || path.Base(name) != name || name == "." || name == ".." {
				return errors.New("invalid memory directory entry")
			}
			p := path.Join(dir, name)
			if isDir {
				if e = walk(p, depth+1); e != nil {
					return e
				}
				continue
			}
			b, e := a.Sandbox.ReadFile(ctx, sid, p, (2<<20)+1)
			if e != nil {
				return e
			}
			total += len(b)
			if len(b) > 2<<20 || total > 20<<20 || len(out) >= 1000 {
				return errors.New("memory mount size exceeded")
			}
			if b == nil {
				b = []byte{}
			}
			out[strings.TrimPrefix(p, root+"/")] = b
		}
		return nil
	}
	e := walk(root, 0)
	return out, e
}
