package execution

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/httpx"
)

// TestTaskViewMatchesModelContract guards the raw-JSONB task read path: the
// response must expose exactly the contract fields the Task model serializes,
// with identical values, while the private worker columns and the messages
// history stay hidden.
func TestTaskViewMatchesModelContract(t *testing.T) {
	db, p, s, key := setupWithKey(t)
	a := agent(t, db, p, "", nil)
	tk := task(t, db, p, s, a)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/v1", httpx.Authenticate(db))
	Register(v, db, nil)
	server := httptest.NewServer(r)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/tasks/"+tk.ID, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	raw, _ := io.ReadAll(res.Body)
	var got map[string]any
	if e := json.Unmarshal(raw, &got); e != nil {
		t.Fatalf("response is not JSON: %v", e)
	}

	// Reference: the model row serialized through the struct path.
	var row Task
	if e := db.Where("id", tk.ID).Take(&row).Error; e != nil {
		t.Fatal(e)
	}
	refBytes, _ := json.Marshal(row)
	var ref map[string]any
	_ = json.Unmarshal(refBytes, &ref)

	for field, want := range ref {
		gotValue, ok := got[field]
		if !ok {
			t.Fatalf("response missing field %q", field)
		}
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(gotValue)
		if string(wantJSON) != string(gotJSON) {
			t.Fatalf("field %q: got %s want %s", field, gotJSON, wantJSON)
		}
	}
	for field := range got {
		if _, ok := ref[field]; !ok {
			t.Fatalf("response has extra field %q", field)
		}
	}
	for _, hidden := range []string{"messages", "owner", "epoch", "lease_until", "finalizing", "failures"} {
		if _, ok := got[hidden]; ok {
			t.Fatalf("private field %q leaked", hidden)
		}
	}
}

// TestTaskViewRawSplicing verifies the heavy JSONB columns pass through as
// raw bytes (no struct round-trip) and that nullable columns stay omitted.
func TestTaskViewRawSplicing(t *testing.T) {
	db, p, s, _ := setupWithKey(t)
	a := agent(t, db, p, "", nil)
	tk := task(t, db, p, s, a)

	var view taskView
	if e := db.Model(&Task{}).Where("id", tk.ID).Take(&view).Error; e != nil {
		t.Fatal(e)
	}
	var agent map[string]any
	if e := json.Unmarshal(view.Snapshot, &agent); e != nil {
		t.Fatalf("snapshot is not raw JSON: %v", e)
	}
	if len(view.Experts) == 0 {
		t.Fatal("experts should carry raw JSON when set")
	}
	if view.Budget == nil || string(view.Budget) == "null" {
		t.Fatalf("budget must never be null, got %s", view.Budget)
	}
	if view.Evaluation != nil {
		t.Fatal("evaluation should be omitted when unset")
	}
}

// TestListTasksUsesLeanProjection asserts the session task list reads an
// explicit column projection instead of SELECT *, so the messages JSONB is
// never dragged through the list path.
func TestListTasksUsesLeanProjection(t *testing.T) {
	db, p, s, key := setupWithKey(t)
	a := agent(t, db, p, "", nil)
	task(t, db, p, s, a)

	var sqls []string
	db.Callback().Query().After("gorm:query").Register("test:capture_sql", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "tasks" {
			sqls = append(sqls, tx.Statement.SQL.String())
		}
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/v1", httpx.Authenticate(db))
	Register(v, db, nil)
	server := httptest.NewServer(r)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/sessions/"+s.ID+"/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if len(sqls) == 0 {
		t.Fatal("no tasks query captured")
	}
	for _, sql := range sqls {
		if strings.Contains(sql, "messages") {
			t.Fatalf("messages column leaked into list query: %s", sql)
		}
		if strings.Contains(sql, "SELECT *") {
			t.Fatalf("unprojected select on tasks: %s", sql)
		}
	}
}

func jsonColumnLeak(sql string) bool {
	// The lean projection names its columns explicitly; any SELECT * would
	// drag the messages JSONB along.
	return len(sql) > 0
}
