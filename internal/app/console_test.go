package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/agents"
	"wave-ai.local/wave/internal/modules/deployments"
	"wave-ai.local/wave/internal/modules/skills"

	"wave-ai.local/wave/internal/modules/console"
	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/xid"
)

func TestConsoleOwnershipPaginationAndImports(t *testing.T) {
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
	defer a.Close()
	org := xid.New("console")
	key, err := auth.Bootstrap(ctx, a.DB, org, "alice", "console", auth.ScopeAPI)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := auth.Bootstrap(ctx, a.DB, org, "bob", "console", auth.ScopeAPI)
	if err != nil {
		t.Fatal(err)
	}
	p, err := auth.Authenticate(ctx, a.DB, key)
	if err != nil {
		t.Fatal(err)
	}
	s := execution.Session{ID: xid.New("session"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Title: "100%_literal"}
	if err = a.DB.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	task := execution.Task{ID: xid.New("task"), SessionID: s.ID, State: "succeeded", AgentID: "console_agent"}
	task.RootID = task.ID
	if err = a.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := 1; i <= 5; i++ {
		kind := "task.state"
		if i == 3 {
			kind = "message.delta"
		}
		e := execution.Event{SessionID: s.ID, Seq: int64(i), TaskID: task.ID, Type: kind, CreatedAt: at, Data: map[string]any{"private": "content"}}
		if err = a.DB.Create(&e).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, e := console.Browse(ctx, a.DB, p, console.Query{Kind: "traces", Limit: 1}); e != nil {
		t.Fatalf("browse: %v", e)
	}
	h := a.Handler()
	request := func(key, path string, status int) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	// New management lists must preserve ownership and omit large or private fields.
	skill := skills.Skill{ID: xid.New("skill"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: "console skill", Description: "test package"}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	entry, _ := zw.Create("SKILL.md")
	_, _ = entry.Write([]byte("---\nname: console skill\ndescription: test package\n---\nTest instructions"))
	_ = zw.Close()
	var upload bytes.Buffer
	mw := multipart.NewWriter(&upload)
	part, _ := mw.CreateFormFile("file", "skill.zip")
	_, _ = part.Write(archive.Bytes())
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/v1/skills", &upload)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != 201 || json.Unmarshal(resp.Body.Bytes(), &skill) != nil {
		t.Fatalf("skill upload: %d %s", resp.Code, resp.Body.String())
	}
	agent := agents.Agent{ID: xid.New("agent"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Version: 1, Config: agents.Config{Name: "100%_console agent", Model: "test", Instructions: "private prompt", SkillIDs: []string{skill.ID}}}
	if err = a.DB.Create(&agent).Error; err != nil {
		t.Fatal(err)
	}
	second := agent
	second.ID = xid.New("agent")
	second.Config = agents.Config{Name: "other", Model: "test"}
	if err = a.DB.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	deployment := deployments.Deployment{ID: xid.New("deployment"), OrgID: p.OrgID, OwnerID: p.PrincipalID, Name: "console deployment", AgentID: agent.ID, Input: "private input"}
	if err = a.DB.Create(&deployment).Error; err != nil {
		t.Fatal(err)
	}
	box := sandbox.Record{SessionID: s.ID, Backend: "gvisor", EngineHost: "private-engine", Endpoint: "private-endpoint", StagerID: "private-stager", BackendID: "instance-test", Name: "test sandbox", State: "stopped", CPUs: 1, MemoryMiB: 512, Image: "test-image"}
	if err = a.DB.Create(&box).Error; err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"skills", "agents", "deployments", "sandboxes"} {
		var list console.List
		body := request(peer, "/v1/console/resources/"+kind, 200)
		if json.Unmarshal(body, &list) != nil || len(list.Data) != 0 {
			t.Fatalf("peer saw %s", kind)
		}
		body = request(key, "/v1/console/resources/"+kind, 200)
		if json.Unmarshal(body, &list) != nil || len(list.Data) == 0 || bytes.Contains(body, []byte("private")) {
			t.Fatalf("invalid %s projection: %s", kind, body)
		}
		request(key, "/v1/console/resources/"+kind+"?limit=201", 400)
		request(key, "/v1/console/resources/"+kind+"?after=bad", 400)
	}
	for kind, id := range map[string]string{"skills": skill.ID, "deployments": deployment.ID, "sandboxes": s.ID} {
		request(peer, "/v1/console/resources/"+kind+"/"+id, 404)
		request("", "/v1/console/resources/"+kind+"/"+id, 401)
		body := request(key, "/v1/console/resources/"+kind+"/"+id, 200)
		if kind == "sandboxes" && bytes.Contains(body, []byte("private")) {
			t.Fatal("sandbox leaked infrastructure")
		}
		if kind == "sandboxes" {
			var record console.Record
			if json.Unmarshal(body, &record) != nil || record.Meta["memory_mib"] != float64(512) || record.Meta["cpus"] != float64(1) {
				t.Fatalf("sandbox allocation projection: %s", body)
			}
		}
		if kind == "skills" && !bytes.Contains(body, []byte("Test instructions")) {
			t.Fatal("missing skill preview")
		}
	}
	var related console.List
	for path, id := range map[string]string{
		"agents?skill_id=" + skill.ID:      agent.ID,
		"agents?q=%25_":                    agent.ID,
		"deployments?agent_id=" + agent.ID: deployment.ID,
		"sandboxes?session_id=" + s.ID:     s.ID,
	} {
		body := request(key, "/v1/console/resources/"+path, 200)
		if json.Unmarshal(body, &related) != nil || len(related.Data) != 1 || related.Data[0].ID != id {
			t.Fatalf("related filter %s: %s", path, body)
		}
	}
	request(key, "/v1/console/resources/deployments?state=bad", 400)
	request(key, "/v1/console/resources/sandboxes?time_field=finished_at", 400)
	var page1, page2 console.List
	_ = json.Unmarshal(request(key, "/v1/console/resources/agents?limit=1", 200), &page1)
	_ = json.Unmarshal(request(key, "/v1/console/resources/agents?limit=1&cursor="+page1.NextCursor, 200), &page2)
	if page1.NextCursor == "" || len(page2.Data) != 1 || page1.Data[0].ID == page2.Data[0].ID || page2.NextCursor != "" {
		t.Fatal("agent cursor pagination")
	}

	for _, kind := range []string{"traces", "tasks", "sessions", "events"} {
		var list console.List
		data := request(peer, "/v1/console/resources/"+kind, 200)
		if json.Unmarshal(data, &list) != nil || len(list.Data) != 0 {
			t.Fatalf("peer saw %s: %s", kind, data)
		}
	}
	request("", "/v1/console/resources/tasks", 401)
	request(peer, "/v1/console/events/"+s.ID+"/1", 404)
	request(key, "/v1/console/resources/events?cursor=bad", 400)
	request(key, "/v1/console/resources/tasks?limit=201", 400)
	request(key, "/v1/console/resources/tasks?before=bad", 400)
	request(key, "/v1/console/resources/nope", 400)
	var log execution.Event
	if err = json.Unmarshal(request(key, "/v1/console/events/"+s.ID+"/1", 200), &log); err != nil || log.Data["private"] != "content" {
		t.Fatal("event detail missing")
	}
	var list console.List
	if err = json.Unmarshal(request(key, "/v1/console/resources/tasks?limit=1", 200), &list); err != nil || len(list.Data) != 1 || list.Data[0].ID != task.ID {
		t.Fatalf("task ownership subquery: %+v %v", list, err)
	}
	if bytes.Contains(request(key, "/v1/console/resources/events", 200), []byte("private")) {
		t.Fatal("list leaked payload")
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		var list console.List
		if err = json.Unmarshal(request(key, "/v1/console/resources/events?limit=2&cursor="+cursor, 200), &list); err != nil {
			t.Fatal(err)
		}
		for _, r := range list.Data {
			if seen[r.ID] {
				t.Fatal("duplicate across tied timestamps")
			}
			seen[r.ID] = true
		}
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 non-delta events, got %v", seen)
	}
	for _, seq := range []int{1, 2, 4, 5} {
		if !seen[s.ID+":"+strconv.Itoa(seq)] {
			t.Fatal("missing log")
		}
	}
	var search console.List
	if err = json.Unmarshal(request(key, "/v1/console/resources/sessions?q=%25_", 200), &search); err != nil || len(search.Data) != 1 {
		t.Fatalf("literal search failed: %+v %v", search, err)
	}
	report := []byte(`{"version":2,"run_id":"bench_test","kind":"live","started_at":"2026-09-14T00:00:00Z","phases":[{"workers":1,"attempted":2,"succeeded":2,"failed":0,"elapsed_seconds":1.2}]}`)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ = writer.CreateFormFile("file", "console-test.json")
	_, _ = part.Write(report)
	_ = writer.Close()
	r := httptest.NewRequest(http.MethodPost, "/v1/console/benchmarks", &body)
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var f files.File
	if err = json.Unmarshal(w.Body.Bytes(), &f); err != nil || f.MIME != console.BenchMIME {
		t.Fatal("bad imported file", err)
	}
	request(peer, "/v1/files/"+f.ID+"/content", 404)
	if string(request(key, "/v1/files/"+f.ID+"/content", 200)) != string(report) {
		t.Fatal("report bytes changed")
	}
	if err = json.Unmarshal(request(key, "/v1/console/resources/benchmarks", 200), &list); err != nil || len(list.Data) != 1 || list.Data[0].ID != f.ID {
		t.Fatal("bench browse failed", err)
	}
}
