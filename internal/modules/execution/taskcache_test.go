package execution

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wave-ai.local/wave/internal/platform/httpx"
)

// TestTaskPollCacheServesHitsWithoutJSONBReads proves a repeated poll of an
// unchanged task runs only the small updated_at probe, and that any write to
// the task (which advances updated_at) forces a fresh full read.
func TestTaskPollCacheServesHitsWithoutJSONBReads(t *testing.T) {
	db, p, s, key := setupWithKey(t)
	a := agent(t, db, p, "", nil)
	tk := task(t, db, p, s, a)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/v1", httpx.Authenticate(db))
	Register(v, db, nil)
	server := httptest.NewServer(r)
	defer server.Close()

	get := func() (map[string]any, []string) {
		var sqls []string
		db.Callback().Query().After("gorm:query").Register("test:cache_sql", func(tx *gorm.DB) {
			if tx.Statement != nil && tx.Statement.Table == "tasks" {
				sqls = append(sqls, tx.Statement.SQL.String())
			}
		})
		defer db.Callback().Query().Remove("test:cache_sql")
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/tasks/"+tk.ID, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
		var m map[string]any
		if e := json.Unmarshal(body, &m); e != nil {
			t.Fatalf("bad JSON: %v", e)
		}
		return m, sqls
	}

	first, firstSQL := get()
	if len(firstSQL) == 0 {
		t.Fatal("miss path must read the task row")
	}
	fullReads := 0
	for _, sql := range firstSQL {
		if strings.Contains(sql, "snapshot") {
			fullReads++
		}
	}
	if fullReads != 1 {
		t.Fatalf("miss path should full-read once, saw %d (sqls: %v)", fullReads, firstSQL)
	}

	second, secondSQL := get()
	for _, sql := range secondSQL {
		if strings.Contains(sql, "snapshot") || strings.Contains(sql, "SELECT *") {
			t.Fatalf("cache hit re-read the payload: %s", sql)
		}
	}
	if second["id"] != first["id"] || second["state"] != first["state"] {
		t.Fatal("cache hit returned different content")
	}

	// Any write advances updated_at and must invalidate the cached body.
	if e := db.Model(&Task{}).Where("id", tk.ID).Update("state", "running").Error; e != nil {
		t.Fatal(e)
	}
	time.Sleep(2 * time.Millisecond)
	third, thirdSQL := get()
	if third["state"] != "running" {
		t.Fatalf("stale cache after write: state=%v", third["state"])
	}
	fullReads = 0
	for _, sql := range thirdSQL {
		if strings.Contains(sql, "snapshot") {
			fullReads++
		}
	}
	if fullReads != 1 {
		t.Fatalf("invalidated poll should full-read once, saw %d", fullReads)
	}
}

// TestTaskCacheLRUEviction keeps the cache bounded: inserting capacity+1
// entries evicts the least recently used one.
func TestTaskCacheLRUEviction(t *testing.T) {
	c := newTaskResponseCache(2)
	c.put("a", time.Unix(1, 0), []byte("a1"))
	c.put("b", time.Unix(1, 0), []byte("b1"))
	if _, ok := c.get("a", time.Unix(1, 0)); !ok {
		t.Fatal("a should be cached")
	}
	c.put("c", time.Unix(1, 0), []byte("c1")) // evicts b (LRU)
	if _, ok := c.get("b", time.Unix(1, 0)); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := c.get("a", time.Unix(1, 0)); !ok {
		t.Fatal("a must survive as recently used")
	}
	if len(c.byKey) != 2 {
		t.Fatalf("cache size %d, want 2", len(c.byKey))
	}
}
