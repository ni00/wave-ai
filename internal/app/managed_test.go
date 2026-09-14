package app

import (
	"context"
	"encoding/json"
	"gorm.io/gorm/clause"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/vault"
	"wave-ai.local/wave/internal/modules/webhooks"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestManagedConfigurationAndDelivery(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	if e := Init(ctx, dsn); e != nil {
		t.Fatal(e)
	}
	a, e := New(ctx, storageTestConfig(t, dsn, "local"))
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
	otherKey, e := auth.Bootstrap(ctx, a.DB, xid.New("org"), "other", "test", auth.ScopeAPI)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		sessions := a.DB.Model(&execution.Session{}).Select("id").Where(clause.Eq{Column: "owner_id", Value: p.PrincipalID})
		a.DB.Model(&execution.Task{}).Where(clause.Expr{SQL: "session_id IN (?)", Vars: []any{sessions}}).Updates(map[string]any{"state": "canceled", "owner": "", "lease_until": nil, "cancel_requested": true})
		a.DB.Model(&execution.Session{}).Where(clause.Eq{Column: "owner_id", Value: p.PrincipalID}).Update("active_root", "")
		a.DB.Model(&deployments.Deployment{}).Where(clause.Eq{Column: "owner_id", Value: p.PrincipalID}).Update("paused", true)
		a.DB.Model(&webhooks.Subscription{}).Where(clause.Eq{Column: "owner_id", Value: p.PrincipalID}).Update("paused", true)
	})
	router := a.Handler()
	request := func(key, method, path, body string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	expert, e := agents.Create(ctx, a.DB, p, agents.Config{Name: "expert", Model: "test", Tools: []agents.Tool{{Name: "lookup", Kind: "custom", Parameters: map[string]any{"type": "object"}}}})
	if e != nil {
		t.Fatal(e)
	}
	parent, e := agents.Create(ctx, a.DB, p, agents.Config{Name: "coordinator", Model: "test", ExpertIDs: []string{expert.ID}, DelegationPolicy: "explicit"})
	if e != nil {
		t.Fatal(e)
	}
	if parent.Config.ExpertVersions[expert.ID] != 1 {
		t.Fatal("expert not pinned")
	}
	updated := expert.Config
	updated.Instructions = "changed"
	if _, e = agents.Update(ctx, a.DB, p, expert.ID, 1, updated); e != nil {
		t.Fatal(e)
	}
	s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID}
	if e = a.DB.Create(&s).Error; e != nil {
		t.Fatal(e)
	}
	task, e := execution.CreateTask(ctx, a.DB, p, s.ID, parent.ID, "run", execution.Budget{})
	if e != nil {
		t.Fatal(e)
	}
	if task.Experts[expert.ID].Version != 1 {
		t.Fatal("task resolved latest expert instead of pinned version")
	}
	child, e := execution.Spawn(ctx, a.DB, p, task.ID, expert.ID, "work", execution.Budget{})
	if e != nil {
		t.Fatal(e)
	}
	if len(child.Snapshot.Tools) != 1 || child.Snapshot.Tools[0].Name != "lookup" {
		t.Fatal("explicit delegation lost expert capability")
	}
	request(otherKey, "GET", "/v1/tasks/"+task.ID+"/collaboration", "", 404)
	raw := request(key, "GET", "/v1/tasks/"+task.ID+"/collaboration", "", 200)
	var coll execution.TasksResponse
	json.Unmarshal(raw, &coll)
	if len(coll.Data) != 2 {
		t.Fatal("collaboration membership")
	}
	t.Run("durable scheduled failure and pinned config", func(t *testing.T) {
		d, e := deployments.Create(ctx, a.DB, p, deployments.CreateRequest{Name: "daily", AgentID: parent.ID, Input: "run", Cron: "0 9 * * *", Timezone: "Asia/Shanghai", Budget: execution.Budget{MaxTokens: 1234}})
		if e != nil {
			t.Fatal(e)
		}
		if d.AgentVersion != 1 || d.Timezone != "Asia/Shanghai" {
			t.Fatal("deployment version/timezone")
		}
		future, e := deployments.Upcoming(d, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC))
		if e != nil || len(future.Times) != 5 || future.Times[0].UTC().Hour() != 1 {
			t.Fatalf("future schedule: %+v %v", future, e)
		}
		c := deployments.CreateRequest{Name: d.Name, AgentID: parent.ID, Input: "new", Cron: d.Cron, Timezone: d.Timezone}
		v, e := deployments.Update(ctx, a.DB, p, d.ID, deployments.UpdateRequest{Version: d.Version, Config: c})
		if e != nil {
			t.Fatal(e)
		}
		if v.Version != 2 {
			t.Fatal("version not incremented")
		}
		if _, e = deployments.Update(ctx, a.DB, p, d.ID, deployments.UpdateRequest{Version: 1, Config: c}); e == nil {
			t.Fatal("stale edit accepted")
		}
		if _, e = deployments.Pause(ctx, a.DB, p, d.ID, true); e != nil {
			t.Fatal(e)
		}
		run, e := deployments.Fire(ctx, a.DB, p, d.ID, false)
		if e != nil || run.Status != "launched" {
			t.Fatalf("paused manual run: %+v %v", run, e)
		}
		var created execution.Task
		a.DB.Where(clause.Eq{Column: "id", Value: run.TaskID}).Take(&created)
		if created.AgentVersion != 1 {
			t.Fatal("deployment agent not pinned")
		}
		// Retrying the same manual request creates one durable run.
		var ids []string
		for i := 0; i < 2; i++ {
			req := httptest.NewRequest("POST", "/v1/deployments/"+d.ID+"/run", nil)
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Idempotency-Key", "manual-retry")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != 202 {
				t.Fatal(w.Body.String())
			}
			var r deployments.Run
			json.Unmarshal(w.Body.Bytes(), &r)
			ids = append(ids, r.ID)
		}
		if ids[0] != ids[1] {
			t.Fatal("manual run was not deduplicated")
		}
		broken, e := deployments.Create(ctx, a.DB, p, deployments.CreateRequest{Name: "broken", AgentID: expert.ID, Input: "run", Cron: "* * * * *"})
		if e != nil {
			t.Fatal(e)
		}
		if e = a.DB.Model(&agents.Agent{}).Where(clause.Eq{Column: "id", Value: expert.ID}).Update("archived", true).Error; e != nil {
			t.Fatal(e)
		}
		if e = a.DB.Model(&broken).Update("next_at", time.Now().Add(-time.Second)).Error; e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := deployments.Fire(ctx, a.DB, p, broken.ID, true)
				if err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		var runs []deployments.Run
		a.DB.Where(clause.Eq{Column: "deployment_id", Value: broken.ID}).Find(&runs)
		if len(runs) != 1 || runs[0].Status != "failed" || runs[0].TaskID != "" || !strings.Contains(runs[0].Reason, "archived") {
			t.Fatalf("failed launch lost: %+v", runs)
		}
		d2, e := deployments.Get(ctx, a.DB, p, broken.ID)
		if e != nil || !d2.Paused || d2.PauseReason == "" {
			t.Fatal("invalid deployment was not paused")
		}
		request(otherKey, "GET", "/v1/deployments/"+d.ID+"/versions", "", 404)
	})
	t.Run("vault scopes rotation and redaction", func(t *testing.T) {
		var v vault.Vault
		json.Unmarshal(request(key, "POST", "/v1/vaults", `{"name":"test"}`, 200), &v)
		var c vault.Credential
		body := `{"name":"mcp","host":"api.example.com","token":"fake-initial-token","vault_id":"` + v.ID + `"}`
		raw := request(key, "POST", "/v1/credentials", body, 201)
		json.Unmarshal(raw, &c)
		if strings.Contains(string(raw), "fake-initial-token") || strings.Contains(string(raw), "ciphertext") {
			t.Fatal("secret leaked")
		}
		if _, e := vault.SessionBearer(ctx, a.DB, a.Box, p, c.ID, "https://api.example.com/mcp", nil); e == nil {
			t.Fatal("unbound vault accepted")
		}
		token, e := vault.SessionBearer(ctx, a.DB, a.Box, p, c.ID, "https://api.example.com/mcp", []string{v.ID})
		if e != nil || token != "fake-initial-token" {
			t.Fatal("bound credential failed")
		}
		c2, e := vault.Rotate(ctx, a.DB, a.Box, p, c.ID, vault.RotateRequest{Version: 1, Token: "fake-rotated-token"})
		if e != nil || c2.Version != 2 {
			t.Fatal("rotation failed", e)
		}
		if _, e = vault.Rotate(ctx, a.DB, a.Box, p, c.ID, vault.RotateRequest{Version: 1, Token: "stale"}); e == nil {
			t.Fatal("stale credential rotation accepted")
		}
		token, e = vault.SessionBearer(ctx, a.DB, a.Box, p, c.ID, "https://api.example.com/mcp", []string{v.ID})
		if e != nil || token != "fake-rotated-token" {
			t.Fatal("rotation not used")
		}
		request(otherKey, "GET", "/v1/vaults/"+v.ID, "", 404)
		request(otherKey, "PUT", "/v1/credentials/"+c.ID, `{"version":2,"token":"other"}`, 404)
		request(key, "DELETE", "/v1/vaults/"+v.ID, "", 200)
		if _, e = vault.SessionBearer(ctx, a.DB, a.Box, p, c.ID, "https://api.example.com/mcp", []string{v.ID}); e == nil {
			t.Fatal("archived vault accepted")
		}
	})
	t.Run("signed durable webhook retry", func(t *testing.T) {
		secret := strings.Repeat("test-signing-key-", 3)
		received := 0
		eventID := ""
		receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			if r.Header.Get("Wave-Signature") != webhooks.Signature([]byte(secret), r.Header.Get("Wave-Timestamp"), raw) {
				t.Error("invalid signature")
			}
			if received > 0 && eventID != r.Header.Get("Wave-Event-ID") {
				t.Error("retry changed event identity")
			}
			eventID = r.Header.Get("Wave-Event-ID")
			received++
			if received == 1 {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
		}))
		defer receiver.Close()
		sub, e := webhooks.Create(ctx, a.DB, a.Box, p, webhooks.CreateRequest{Name: "receiver", URL: receiver.URL, Events: []string{"task.finished"}, Secret: secret})
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 2; i++ {
			if e = webhooks.Enqueue(a.DB, p, "stable-event", "task.finished", map[string]any{"task_id": task.ID}); e != nil {
				t.Fatal(e)
			}
		}
		var count int64
		a.DB.Model(&webhooks.Delivery{}).Where(clause.Eq{Column: "subscription_id", Value: sub.ID}).Count(&count)
		if count != 1 {
			t.Fatal("duplicate webhook outbox")
		}
		if _, e = webhooks.DeliverOne(ctx, a.DB, a.Box, receiver.Client()); e != nil {
			t.Fatal(e)
		}
		if e = a.DB.Model(&webhooks.Delivery{}).Where(clause.Eq{Column: "subscription_id", Value: sub.ID}).Update("next_at", time.Now().Add(-time.Second)).Error; e != nil {
			t.Fatal(e)
		}
		if _, e = webhooks.DeliverOne(ctx, a.DB, a.Box, receiver.Client()); e != nil {
			t.Fatal(e)
		}
		var d webhooks.Delivery
		a.DB.Where(clause.Eq{Column: "subscription_id", Value: sub.ID}).Take(&d)
		if d.Status != "delivered" || d.Attempts != 2 || received != 2 {
			t.Fatalf("delivery: %+v, received=%d", d, received)
		}
		request(otherKey, "GET", "/v1/webhooks/"+sub.ID+"/deliveries", "", 404)
	})
	t.Run("artifact acceptance is distinct from execution", func(t *testing.T) {
		snapshot := task
		snapshot.Snapshot.Acceptance = []agents.AcceptanceCheck{{Kind: "json", Path: "summary.json", Required: []string{"total"}, Types: map[string]string{"total": "number"}}}
		result := a.evaluate(ctx, s, snapshot)
		if result.Status != "failed" {
			t.Fatal("missing artifact passed")
		}
		if _, e := files.PutArtifact(ctx, a.DB, a.Blobs, p, s.ID, task.ID, "summary.json", []byte(`{"total":12}`)); e != nil {
			t.Fatal(e)
		}
		result = a.evaluate(ctx, s, snapshot)
		if result.Status != "passed" || snapshot.State != "queued" {
			t.Fatal("acceptance changed execution status")
		}
	})
}
func TestAcceptanceJSON(t *testing.T) {
	check := agents.AcceptanceCheck{Kind: "json", Required: []string{"total"}, Types: map[string]string{"total": "number"}}
	for _, raw := range []string{`{"total":"12"}`, `{}`, `null`, `{"total":12} trailing`} {
		if checkJSON([]byte(raw), check) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if e := checkJSON([]byte(`{"total":12}`), check); e != nil {
		t.Fatal(e)
	}
}
