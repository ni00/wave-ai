package sandbox

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Router pins the backend on first reservation; changing the default only
// affects new sessions. Missing backends never fall back to weaker isolation.
type Router struct {
	Store    *gorm.DB
	Default  string
	Backends map[string]Managed
}

func (r *Router) String() string { return r.Default }
func (r *Router) Close() error {
	var result error
	for _, p := range r.Backends {
		if c, ok := p.(interface{ Close() error }); ok {
			result = errors.Join(result, c.Close())
		}
	}
	return result
}
func (r *Router) selected(db *gorm.DB, sid, requested string) (Managed, error) {
	var record Record
	err := db.Where(clause.Eq{Column: "session_id", Value: sid}).Take(&record).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil {
		requested = record.Backend
	}
	if requested == "" {
		requested = r.Default
	}
	p, ok := r.Backends[requested]
	if !ok {
		return nil, fmt.Errorf("sandbox backend %q is not configured", requested)
	}
	return p, nil
}
func (r *Router) ReserveSession(tx *gorm.DB, sid, backend, profile string) error {
	p, err := r.selected(tx, sid, backend)
	if err != nil {
		return err
	}
	resources, err := p.Resources(profile)
	if err != nil {
		return err
	}
	return p.Reserve(tx, sid, resources)
}
func (r *Router) provider(ctx context.Context, sid string) (Managed, error) {
	return r.selected(r.Store.WithContext(ctx), sid, "")
}
func (r *Router) Ensure(ctx context.Context, sid string) error {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return err
	}
	return p.Ensure(ctx, sid)
}
func (r *Router) Identity(ctx context.Context, sid string) (string, error) {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return "", err
	}
	return p.Identity(ctx, sid)
}
func (r *Router) Exec(ctx context.Context, sid string, req ExecRequest) (*ExecResult, error) {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return nil, err
	}
	return p.Exec(ctx, sid, req)
}
func (r *Router) ReadFile(ctx context.Context, sid, path string, maxBytes int64) ([]byte, error) {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return nil, err
	}
	return p.ReadFile(ctx, sid, path, maxBytes)
}
func (r *Router) WriteFile(ctx context.Context, sid, path string, content []byte) error {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return err
	}
	return p.WriteFile(ctx, sid, path, content)
}
func (r *Router) ListDir(ctx context.Context, sid, path string) ([]string, error) {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return nil, err
	}
	return p.ListDir(ctx, sid, path)
}
func (r *Router) StageInput(ctx context.Context, sid, path string, content []byte) error {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return err
	}
	return p.StageInput(ctx, sid, path, content)
}
func (r *Router) StageSkill(ctx context.Context, sid, name string, files map[string][]byte) error {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return err
	}
	return p.StageSkill(ctx, sid, name, files)
}
func (r *Router) StageMemory(ctx context.Context, sid, mountPath, relPath string, content []byte, writable bool) error {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return err
	}
	return p.StageMemory(ctx, sid, mountPath, relPath, content, writable)
}
func (r *Router) VisitOutputs(ctx context.Context, sid string, maxBytes int64, visit func(string, []byte) error) error {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return err
	}
	return p.VisitOutputs(ctx, sid, maxBytes, visit)
}
func (r *Router) Stop(ctx context.Context, sid string) error {
	p, err := r.provider(ctx, sid)
	if err != nil {
		return err
	}
	return p.Stop(ctx, sid)
}
