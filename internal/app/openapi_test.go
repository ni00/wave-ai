package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-openapi/spec"

	"wave-ai.local/wave/internal/modules/execution"
	"wave-ai.local/wave/internal/platform/apierr"
	"wave-ai.local/wave/internal/platform/httpx"
)

func openAPIDoc(t *testing.T) (*gin.Engine, spec.Swagger) {
	t.Helper()
	router := (&App{}).Handler().(*gin.Engine)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("Swagger endpoint: %d %s", w.Code, w.Body.String())
	}
	var doc spec.Swagger
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return router, doc
}

func operations(item spec.PathItem) map[string]*spec.Operation {
	return map[string]*spec.Operation{
		"GET": item.Get, "POST": item.Post, "PUT": item.Put, "PATCH": item.Patch,
		"DELETE": item.Delete, "HEAD": item.Head, "OPTIONS": item.Options,
	}
}

func TestOpenAPIRoutes(t *testing.T) {
	router, doc := openAPIDoc(t)
	if doc.Swagger != "2.0" {
		t.Fatalf("unexpected specification version %q", doc.Swagger)
	}
	documented := map[string]bool{}
	ids := map[string]string{}
	for path, item := range doc.Paths.Paths {
		for method, op := range operations(item) {
			if op == nil {
				continue
			}
			key := method + " " + path
			documented[key] = true
			if prior, exists := ids[op.ID]; op.ID == "" || exists {
				t.Errorf("%s: missing/duplicate operationId %q (also %s)", key, op.ID, prior)
			}
			ids[op.ID] = key
			if strings.HasPrefix(path, "/v1/") {
				if len(op.Security) != 1 {
					t.Errorf("%s: missing security", key)
				} else if _, ok := op.Security[0]["BearerAuth"]; !ok {
					t.Errorf("%s: missing BearerAuth", key)
				}
				for _, status := range []int{401, 403, 500} {
					if op.Responses == nil || op.Responses.StatusCodeResponses[status].Schema == nil {
						t.Errorf("%s: missing %d error schema", key, status)
					}
				}
			}
			for _, p := range op.Parameters {
				if p.In == "query" && p.Name == "limit" {
					if p.Minimum == nil || *p.Minimum != 1 || p.Maximum == nil || *p.Maximum != 200 || p.Default != float64(100) {
						t.Errorf("%s: incomplete page limit contract", key)
					}
				}
			}
		}
	}
	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/swagger/") || strings.HasPrefix(route.Path, "/console/") {
			continue
		}
		parts := strings.Split(route.Path, "/")
		for i, part := range parts {
			if name, ok := strings.CutPrefix(part, ":"); ok {
				parts[i] = "{" + name + "}"
			}
		}
		key := route.Method + " " + strings.Join(parts, "/")
		if !documented[key] {
			t.Errorf("registered route missing from OpenAPI: %s", key)
		}
		delete(documented, key)
	}
	for key := range documented {
		t.Errorf("OpenAPI documents an unregistered route: %s", key)
	}
	if op := doc.Paths.Paths["/health"].Get; op == nil || len(op.Security) != 0 {
		t.Error("health must be documented without authentication")
	}
}

func TestOpenAPI3Publication(t *testing.T) {
	router, legacy := openAPIDoc(t)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil))
	var doc struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || w.Code != 200 || doc.OpenAPI != "3.0.0" {
		t.Fatalf("published OpenAPI 3: %d %v", w.Code, err)
	}
	for path, item := range legacy.Paths.Paths {
		for method, op := range operations(item) {
			if op != nil && doc.Paths[path][strings.ToLower(method)].OperationID != op.ID {
				t.Errorf("OpenAPI versions disagree for %s %s", method, path)
			}
		}
	}
}

func TestOpenAPILanguages(t *testing.T) {
	router := (&App{}).Handler()
	read := func(path string) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, w.Code)
		}
		return w.Body.Bytes()
	}
	english := read("/swagger/openapi.json")
	if string(english) != string(read("/swagger/openapi.en.json")) {
		t.Fatal("English alias differs from the default contract")
	}
	chinese := read("/swagger/openapi.zh-CN.json")
	for _, pair := range []struct {
		data    []byte
		summary string
	}{
		{chinese, "创建任务"}, {english, "Create a task"},
	} {
		var doc map[string]any
		if err := json.Unmarshal(pair.data, &doc); err != nil {
			t.Fatal(err)
		}
		operation := doc["paths"].(map[string]any)["/v1/sessions/{id}/tasks"].(map[string]any)["post"].(map[string]any)
		if operation["summary"] != pair.summary {
			t.Fatalf("wrong language summary: %v", operation["summary"])
		}
	}
	// Documentation can differ; every other value, including examples, must match.
	var strip func(any) any
	strip = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			out := map[string]any{}
			for key, child := range value {
				if _, text := child.(string); text && (key == "description" || key == "summary") {
					continue
				}
				switch key {
				case "example", "examples", "default", "enum", "const":
					out[key] = child
				default:
					out[key] = strip(child)
				}
			}
			return out
		case []any:
			for index, child := range value {
				value[index] = strip(child)
			}
		}
		return value
	}
	var zh, en any
	if err := json.Unmarshal(chinese, &zh); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(english, &en); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(strip(zh), strip(en)) {
		t.Fatal("language versions differ outside documentation")
	}
	for _, language := range []string{"zh-CN", "en"} {
		page := string(read("/swagger/index.html?lang=" + language))
		if !strings.Contains(page, "./swagger-initializer.js") {
			t.Fatalf("Swagger UI did not load its language selector for %s", language)
		}
	}
	initializer := string(read("/swagger/swagger-initializer.js"))
	for _, value := range []string{"window.location.search", "/swagger/openapi.json", "/swagger/openapi.zh-CN.json", "urls.primaryName"} {
		if !strings.Contains(initializer, value) {
			t.Fatalf("published initializer missing %s", value)
		}
	}
}

func definition(t *testing.T, doc spec.Swagger, suffix string) spec.Schema {
	t.Helper()
	for name, schema := range doc.Definitions {
		if strings.HasSuffix(name, suffix) {
			return schema
		}
	}
	t.Fatalf("missing definition %s", suffix)
	return spec.Schema{}
}

func TestOpenAPIContracts(t *testing.T) {
	_, doc := openAPIDoc(t)
	// Check every reference, including deeply nested fields used by SDK generators.
	docJSON, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(docJSON, &document); err != nil {
		t.Fatal(err)
	}
	var checkRefs func(any)
	checkRefs = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if ref, ok := value["$ref"].(string); ok {
				name, local := strings.CutPrefix(ref, "#/definitions/")
				if _, exists := doc.Definitions[name]; !local || !exists {
					t.Errorf("unresolved schema reference %q", ref)
				}
			}
			for _, child := range value {
				checkRefs(child)
			}
		case []any:
			for _, child := range value {
				checkRefs(child)
			}
		}
	}
	checkRefs(document)
	for name, fields := range map[string][]string{
		"execution.TaskRequest":      {"agent_id", "input"},
		"execution.TextRequest":      {"text"},
		"execution.ReconcileRequest": {"confirm_stopped"},
		"agents.UpdateRequest":       {"version", "config"},
	} {
		schema := definition(t, doc, name)
		for _, field := range fields {
			if !slices.Contains(schema.Required, field) {
				t.Errorf("%s.%s must be required", name, field)
			}
		}
	}
	// Compare the actual JSON representation of unknown usage with the public schema.
	raw, err := json.Marshal(execution.Generation{State: "interrupted"})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	generation := definition(t, doc, "execution.Generation")
	for _, field := range []string{"duration_ms", "first_delta_ms"} {
		p := generation.Properties[field]
		if v, present := wire[field]; !present || v != nil || p.Extensions["x-nullable"] != true || p.Format != "int64" || !slices.Contains(generation.Required, field) {
			t.Errorf("%s: unknown must be a required, nullable int64", field)
		}
	}
	usage := definition(t, doc, "modelclient.Usage")
	for field, value := range wire["usage"].(map[string]any) {
		if value != nil || usage.Properties[field].Extensions["x-nullable"] != true || !slices.Contains(usage.Required, field) {
			t.Errorf("usage.%s: unknown must remain null", field)
		}
	}
	if _, present := wire["finished_at"]; present || slices.Contains(generation.Required, "finished_at") || generation.Properties["finished_at"].Format != "date-time" {
		t.Error("unfinished generation must omit finished_at, with date-time format when present")
	}
	tool := definition(t, doc, "execution.ToolCall")
	if !slices.Contains(tool.Properties["status"].Enum, any("waiting_children")) {
		t.Error("tool status is missing waiting_children")
	}
	for _, path := range []string{"/v1/sessions/{id}/messages", "/v1/sessions/{id}/events", "/v1/sessions/{id}/events/stream"} {
		op := doc.Paths.Paths[path].Get
		if op == nil || !slices.ContainsFunc(op.Parameters, func(p spec.Parameter) bool { return p.In == "header" && p.Name == "Last-Event-ID" }) {
			t.Errorf("%s: missing resume header", path)
		}
	}
}

func TestOpenAPIDownload(t *testing.T) {
	_, doc := openAPIDoc(t)
	modified := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	for _, path := range []string{"/v1/files/{id}/content", "/v1/skills/{id}/content"} {
		op := doc.Paths.Paths[path].Get
		for _, tc := range []struct {
			header, value string
			status        int
		}{
			{"", "", 200}, {"Range", "bytes=1-2", 206}, {"Range", "bytes=100-200", 416},
			{"If-Modified-Since", modified.Format(http.TimeFormat), 304},
			{"If-Unmodified-Since", modified.Add(-time.Hour).Format(http.TimeFormat), 412},
		} {
			req := httptest.NewRequest(http.MethodGet, "/content", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			w := httptest.NewRecorder()
			httpx.Download(w, req, "test.bin", modified, strings.NewReader("abcdef"))
			response, exists := op.Responses.StatusCodeResponses[w.Code]
			if w.Code != tc.status || !exists {
				t.Fatalf("%s: download status %d is unexpected or undocumented", path, w.Code)
			}
			switch tc.status {
			case 200, 206:
				if response.Schema == nil || !response.Schema.Type.Contains("file") {
					t.Errorf("%s: binary response must have file schema", path)
				}
			case 304, 412:
				if w.Body.Len() != 0 || response.Schema != nil {
					t.Error("conditional response must be empty")
				}
			case 416:
				if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || response.Schema == nil || !response.Schema.Type.Contains("string") {
					t.Error("Range errors must document their plain-text body")
				}
			}
		}
	}
}

func TestRoutingErrors(t *testing.T) {
	router := (&App{}).Handler()
	for _, tc := range []struct {
		method, path string
		status       int
		typ          apierr.ErrorType
	}{
		{"GET", "/v1/does-not-exist", 404, apierr.NotFound},
		{"PATCH", "/v1/agents", 405, apierr.InvalidRequest},
		{"GET", "/v1/agents", 401, apierr.Authentication},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		var envelope apierr.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || envelope.Type != "error" || envelope.Error.Type != string(tc.typ) || envelope.Error.Message == "" || !strings.HasPrefix(envelope.RequestID, "req_") {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
