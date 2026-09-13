package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"wave-ai.local/wave/internal/modules/vault"

	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/platform/auth"

	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/memory"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestResourceTransactions(t *testing.T) {
	for _, backend := range []string{"local", "s3"} {
		t.Run(backend, func(t *testing.T) { testResourceTransactions(t, backend) })
	}
}
func testResourceTransactions(t *testing.T, backend string) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	if e := Init(ctx, dsn); e != nil {
		t.Fatal(e)
	}
	a, e := New(ctx, storageTestConfig(t, dsn, backend))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	key, e := auth.Bootstrap(ctx, a.DB, xid.New("org"), "owner", "test", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	p, e := auth.Authenticate(ctx, a.DB, key)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		sessions := []execution.Session{}
		auth.Owned(a.DB, p).Find(&sessions)
		for _, session := range sessions {
			a.DB.Model(&execution.Task{}).Where(clause.Eq{Column: "session_id", Value: session.ID}).Update("state", "canceled")
		}
	})
	env := environments.Environment{ID: xid.New("env"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: "test"}
	if e = a.DB.Create(&env).Error; e != nil {
		t.Fatal(e)
	}
	store := memory.Store{ID: xid.New("memory"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: "test"}
	if e = a.DB.Create(&store).Error; e != nil {
		t.Fatal(e)
	}
	s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID, EnvironmentID: env.ID, MemoryIDs: []string{store.ID}}
	if e = a.DB.Create(&s).Error; e != nil {
		t.Fatal(e)
	}
	agent, e := agents.Create(ctx, a.DB, p, agents.Config{Name: "test", Model: "test"})
	if e != nil {
		t.Fatal(e)
	}
	task, e := execution.CreateTask(ctx, a.DB, p, s.ID, agent.ID, "test", execution.Budget{})
	if e != nil {
		t.Fatal(e)
	}
	// This resource test invokes preparation/finalization directly, not workers.
	defer a.DB.Model(&execution.Task{}).Where(clause.Eq{Column: "id", Value: task.ID}).Update("state", "canceled")
	write := func(path, body string, version int) {
		t.Helper()
		if _, e := memory.Write(ctx, a.DB, p, store.ID, path, body, s.ID, version); e != nil {
			t.Fatal(e)
		}
	}
	write("unchanged.txt", "baseline", 0)
	write("changed.txt", "baseline", 0)
	write("deleted.txt", "baseline", 0)
	write("resurrect.txt", "old", 0)
	if _, e = memory.Delete(ctx, a.DB, p, store.ID, "resurrect.txt", s.ID, 1); e != nil {
		t.Fatal(e)
	}
	if e = a.prepare(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Sandbox.Exec(ctx, s.ID, sandbox.ExecRequest{Command: "rm -- " + quote("/mnt/memory/"+store.ID+"/deleted.txt"), RequestID: "delete-test"}); e != nil {
		t.Fatal(e)
	}
	if e = a.Sandbox.WriteFile(ctx, s.ID, "/mnt/memory/"+store.ID+"/resurrect.txt", []byte("restored")); e != nil {
		t.Fatal(e)
	}
	write("unchanged.txt", "concurrent remote", 1)
	if e = a.Sandbox.WriteFile(ctx, s.ID, "/mnt/memory/"+store.ID+"/changed.txt", []byte("local edit")); e != nil {
		t.Fatal(e)
	}
	if e = a.Sandbox.WriteFile(ctx, s.ID, "/mnt/memory/"+store.ID+"/new.txt", []byte{}); e != nil {
		t.Fatal(e)
	}
	if e = a.finish(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	check := func(path, body string, version int) {
		t.Helper()
		var row memory.Entry
		if e := a.DB.Where(clause.And(clause.Eq{Column: "store_id", Value: store.ID}, clause.Eq{Column: "path", Value: path})).Take(&row).Error; e != nil {
			t.Fatal(e)
		}
		if row.Content != body || row.Version != version {
			t.Fatalf("%s = %+v", path, row)
		}
	}
	check("unchanged.txt", "concurrent remote", 2)
	check("changed.txt", "local edit", 2)
	check("new.txt", "", 1)
	check("resurrect.txt", "restored", 3)
	var deleted memory.Entry
	if e = a.DB.Where(clause.And(clause.Eq{Column: "store_id", Value: store.ID}, clause.Eq{Column: "path", Value: "deleted.txt"})).Take(&deleted).Error; e != nil {
		t.Fatal(e)
	}
	if !deleted.Deleted || deleted.Version != 2 {
		t.Fatal("memory deletion not written back", deleted)
	}
	if e = a.finish(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	check("changed.txt", "local edit", 2)
	// Re-stage a new root, then edit the same file locally and remotely.
	task.RootID = xid.New("task")
	if e = a.prepare(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	write("changed.txt", "remote winner", 2)
	if e = a.Sandbox.WriteFile(ctx, s.ID, "/mnt/memory/"+store.ID+"/changed.txt", []byte("conflicting local")); e != nil {
		t.Fatal(e)
	}
	if e = a.finish(ctx, s, task); e == nil {
		t.Fatal("expected retained conflict")
	}
	check("changed.txt", "remote winner", 3)
	var conflicts []memory.Conflict
	if e = a.DB.Where(clause.Eq{Column: "store_id", Value: store.ID}).Find(&conflicts).Error; e != nil {
		t.Fatal(e)
	}
	if len(conflicts) != 1 || conflicts[0].Content != "conflicting local" {
		t.Fatal(conflicts)
	}
	first, e := files.PutArtifact(ctx, a.DB, a.Blobs, p, s.ID, task.ID, "a/result.txt", []byte("one"))
	if e != nil {
		t.Fatal(e)
	}
	same, e := files.PutArtifact(ctx, a.DB, a.Blobs, p, s.ID, task.ID, "a/result.txt", []byte("one"))
	if e != nil || same.ID != first.ID {
		t.Fatalf("idempotency: %v %v", same, e)
	}
	other, e := files.PutArtifact(ctx, a.DB, a.Blobs, p, s.ID, task.ID, "b/result.txt", []byte("one"))
	if e != nil || other.ID == first.ID {
		t.Fatal("basename collision", e)
	}
	changed, e := files.PutArtifact(ctx, a.DB, a.Blobs, p, s.ID, task.ID, "a/result.txt", []byte("two"))
	if e != nil || changed.ID == first.ID {
		t.Fatal("changed content lost", e)
	}
	t.Run("MCP cached credentials remain revocable", func(t *testing.T) { testCachedMCPCredentials(t, a, p) })
	t.Run("package preparation reuse and invalidation", func(t *testing.T) { testPackagePreparation(t, a, p) })
	t.Run("scheduler concurrency and missed slots", func(t *testing.T) {
		due := time.Now().Add(-time.Second)
		d := deployments.Deployment{ID: xid.New("deployment"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: "cron", AgentID: agent.ID, Input: "test", Cron: "* * * * *", NextAt: &due}
		if e := a.DB.Create(&d).Error; e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() {
				if _, e := deployments.Fire(ctx, a.DB, p, d.ID, true); e != nil {
					t.Error(e)
				}
			})
		}
		wg.Wait()
		var count int64
		a.DB.Model(&deployments.Run{}).Where(clause.Eq{Column: "deployment_id", Value: d.ID}).Count(&count)
		if count != 1 {
			t.Fatalf("scheduled runs=%d", count)
		}
		if e := a.DB.Model(&deployments.Deployment{}).Where(clause.Eq{Column: "id", Value: d.ID}).Update("next_at", time.Now().Add(-time.Hour)).Error; e != nil {
			t.Fatal(e)
		}
		missed, e := deployments.Fire(ctx, a.DB, p, d.ID, true)
		if e != nil || missed.TaskID != "" || missed.Reason != "missed schedule skipped" {
			t.Fatalf("missed run: %+v %v", missed, e)
		}
	})
}

type packageSandbox struct {
	sandbox.Provider
	identity string
	installs int
	fail     bool
}

func (p *packageSandbox) Identity(context.Context, string) (string, error) { return p.identity, nil }
func (p *packageSandbox) Exec(ctx context.Context, sid string, r sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	p.installs++
	if p.fail {
		return &sandbox.ExecResult{ExitCode: 1, Stderr: "installation failed"}, nil
	}
	return &sandbox.ExecResult{}, nil
}
func testPackagePreparation(t *testing.T, a *App, p *auth.Principal) {
	ctx := context.Background()
	box := &packageSandbox{Provider: a.Sandbox, identity: "disk-one"}
	original := a.Sandbox
	a.Sandbox = box
	defer func() { a.Sandbox = original }()
	env := environments.Environment{ID: xid.New("env"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: "packages", Packages: map[string][]string{"pip": {"example==1"}}}
	if e := a.DB.Create(&env).Error; e != nil {
		t.Fatal(e)
	}
	s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID, EnvironmentID: env.ID}
	if e := a.DB.Create(&s).Error; e != nil {
		t.Fatal(e)
	}
	task := execution.Task{ID: xid.New("task"), RootID: xid.New("root"), SessionID: s.ID}
	for range 2 {
		if e := a.prepare(ctx, s, task); e != nil {
			t.Fatal(e)
		}
	}
	if box.installs != 1 {
		t.Fatal("same root reinstalled", box.installs)
	}
	task.RootID = xid.New("root")
	if e := a.prepare(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	if box.installs != 1 {
		t.Fatal("next root reinstalled retained dependencies", box.installs)
	}
	box.identity = "replacement-disk"
	task.RootID = xid.New("root")
	if e := a.prepare(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	if box.installs != 2 {
		t.Fatal("new disk skipped setup", box.installs)
	}
	env.Packages["pip"] = []string{"example==2"}
	if e := a.DB.Save(&env).Error; e != nil {
		t.Fatal(e)
	}
	task.RootID = xid.New("root")
	box.fail = true
	if e := a.prepare(ctx, s, task); e == nil {
		t.Fatal("failed install accepted")
	}
	if e := a.prepare(ctx, s, task); e == nil || box.installs != 3 {
		t.Fatal("unconfirmed preparation retried", e, box.installs)
	}
	// Equivalent to the explicit reconciliation after external operations stop.
	if e := a.DB.Model(&Workspace{}).Where(clause.Eq{Column: "session_id", Value: s.ID}).Update("state", "").Error; e != nil {
		t.Fatal(e)
	}
	box.fail = false
	if e := a.prepare(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	if box.installs != 4 {
		t.Fatal("failed setup was cached as ready", box.installs)
	}
	task.RootID = xid.New("root")
	if e := a.prepare(ctx, s, task); e != nil {
		t.Fatal(e)
	}
	if box.installs != 4 {
		t.Fatal("successful setup not reused", box.installs)
	}
}

func testCachedMCPCredentials(t *testing.T, a *App, p *auth.Principal) {
	ctx := context.Background()
	var calls, initializes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch body["method"] {
		case "initialize":
			initializes.Add(1)
			w.Header().Set("Mcp-Session-Id", "fixture-session")
			json.NewEncoder(w).Encode(map[string]any{"id": body["id"], "result": map[string]any{"protocolVersion": "2025-11-25"}})
		case "notifications/initialized":
			w.WriteHeader(202)
		case "tools/call":
			calls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"id": body["id"], "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "confirmed tool failure"}}, "isError": true}})
		}
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	ciphertext, nonce, e := a.Box.Seal([]byte("fixture-token"))
	if e != nil {
		t.Fatal(e)
	}
	cred := vault.Credential{ID: xid.New("credential"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Host: endpoint.Host, Name: "fixture", Ciphertext: ciphertext, Nonce: nonce}
	if e = a.DB.Create(&cred).Error; e != nil {
		t.Fatal(e)
	}
	s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID}
	scope := s.OrgID + "/" + s.OwnerID + "/" + s.ID
	client := a.MCP.Get(scope, "fixture", server.URL, "Bearer fixture-token")
	client.HTTP = server.Client()
	call := execution.ToolCall{Tool: agents.Tool{Name: "fixture", Kind: "mcp", ServerURL: server.URL, CredentialID: cred.ID}, Arguments: "{}"}
	for range 2 {
		result, e := a.execute(ctx, s, call)
		if e != nil || !result.IsError || result.ErrorCode != "mcp_tool_failed" {
			t.Fatal("MCP failure mapping", result, e)
		}
	}
	if initializes.Load() != 1 || calls.Load() != 2 {
		t.Fatal("MCP initialization repeated", initializes.Load(), calls.Load())
	}
	if e = a.DB.Model(&cred).Update("revoked", true).Error; e != nil {
		t.Fatal(e)
	}
	result, e := a.execute(ctx, s, call)
	if e != nil || result.ErrorCode != "credential_unavailable" || calls.Load() != 2 {
		t.Fatal("revoked cached credential used", result, e, calls.Load())
	}
	cred.Revoked = false
	cred.Ciphertext, cred.Nonce, e = a.Box.Seal([]byte("rotated-fixture-token"))
	if e != nil {
		t.Fatal(e)
	}
	if e = a.DB.Save(&cred).Error; e != nil {
		t.Fatal(e)
	}
	replacement := a.MCP.Get(scope, "fixture", server.URL, "Bearer rotated-fixture-token")
	replacement.HTTP = server.Client()
	if replacement == client {
		t.Fatal("rotated credential reused old connection")
	}
	if _, e = a.execute(ctx, s, call); e != nil {
		t.Fatal(e)
	}
	if initializes.Load() != 2 || calls.Load() != 3 {
		t.Fatal("rotated credential did not reinitialize")
	}
	s.OwnerID = "00000000-0000-4000-8000-000000000001"
	result, e = a.execute(ctx, s, call)
	if e != nil || result.ErrorCode != "credential_unavailable" || calls.Load() != 3 {
		t.Fatal("cross-owner credential reuse", e, calls.Load())
	}
}
