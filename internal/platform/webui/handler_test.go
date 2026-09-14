package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedConsole(t *testing.T) {
	req := httptest.NewRequest("GET", "/console/", nil)
	w := httptest.NewRecorder()
	serve(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<title>Wave AI`) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("invalid console response")
	}
	req = httptest.NewRequest("GET", "/console/", nil)
	req.Header.Set("If-None-Match", w.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	serve(cached, req)
	if cached.Code != 304 {
		t.Fatal("ETag failed")
	}
	req = httptest.NewRequest("GET", "/console/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	compressed := httptest.NewRecorder()
	serve(compressed, req)
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	reader.Close()
	if !bytes.Equal(data, w.Body.Bytes()) {
		t.Fatal("compressed asset differs")
	}
	for _, path := range []string{"/console/assets/missing.js", "/console/../secret", "/console/not-found"} {
		w := httptest.NewRecorder()
		serve(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("%s did not return 404", path)
		}
	}
}
