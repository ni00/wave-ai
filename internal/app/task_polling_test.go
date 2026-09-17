package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"gorm.io/gorm/clause"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestTaskPollingOwnershipAndRevocation(t *testing.T) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	if err := Init(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, storageTestConfig(t, dsn, "local"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	org := xid.New("org")
	key, err := auth.Bootstrap(ctx, a.DB, org, "owner", "test", auth.ScopeAPI)
	if err != nil {
		t.Fatal(err)
	}
	p, err := auth.Authenticate(ctx, a.DB, key)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := auth.Bootstrap(ctx, a.DB, org, "peer", "test", auth.ScopeAPI)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := auth.Bootstrap(ctx, a.DB, xid.New("org"), "owner", "test", auth.ScopeAPI)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := agents.Create(ctx, a.DB, p, agents.Config{Name: "poll", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	router := a.Handler()
	// Multiple sessions catch scalar subqueries that accidentally omit correlation.
	for range 2 {
		s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID}
		if err = a.DB.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		task, err := execution.CreateTask(ctx, a.DB, p, s.ID, agent.ID, "private input", execution.Budget{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			a.DB.Model(&execution.Task{}).Where(clause.Eq{Column: "id", Value: task.ID}).Update("state", "canceled")
		})
		check := func(key, id string, status int) {
			t.Helper()
			req := httptest.NewRequest("GET", "/v1/tasks/"+id, nil)
			req.Header.Set("Authorization", "Bearer "+key)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
			}
			if status == 200 {
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body["id"] != task.ID || body["session_id"] != s.ID {
					t.Fatal("task fields changed")
				}
				if _, ok := body["messages"]; ok {
					t.Fatal("private messages exposed")
				}
			}
		}
		check(key, task.ID, 200)
		check(peer, task.ID, 404)
		check(foreign, task.ID, 404)
		check(key, "missing", 404)
		// The key has already been used successfully: revocation must affect the next poll.
		if err = a.DB.Model(&auth.APIKey{}).Where(clause.Eq{Column: "id", Value: p.APIKeyID}).Update("revoked_at", time.Now()).Error; err != nil {
			t.Fatal(err)
		}
		check(key, task.ID, 401)
		if err = a.DB.Model(&auth.APIKey{}).Where(clause.Eq{Column: "id", Value: p.APIKeyID}).Update("revoked_at", nil).Error; err != nil {
			t.Fatal(err)
		}
	}
}
