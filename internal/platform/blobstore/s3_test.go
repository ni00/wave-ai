package blobstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestS3ProtocolFailures(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	var mode atomic.Int32
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("unsigned S3 request")
		}
		if !strings.HasPrefix(r.URL.Path, "/private-bucket/wave/org/alice/") {
			t.Error("incorrect object namespace")
		}
		switch mode.Load() {
		case 1:
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
			return
		case 2:
			w.Header().Set("Content-Length", "16")
			fmt.Fprint(w, "short")
			return
		}
		w.Header().Set("Content-Length", "16")
		w.Header().Set("ETag", `"immutable"`)
		// Deliberately ignores Range to exercise rejection of incompatible servers.
		fmt.Fprint(w, "0123456789abcdef")
	}))
	defer endpoint.Close()
	store, err := NewS3(context.Background(), S3Options{Bucket: "private-bucket", Region: "us-east-1", Endpoint: endpoint.URL, Prefix: "wave", PathStyle: true, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{"org", "alice"}
	key := "org/alice/" + strings.Repeat("a", 64)
	if _, err := store.Open(context.Background(), Scope{"org", "bob"}, key); err == nil {
		t.Fatal("scope bypass")
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized access reached S3")
	}
	mode.Store(1)
	if _, err := store.Open(context.Background(), scope, key); err == nil {
		t.Fatal("access denied ignored")
	}
	mode.Store(2)
	f, err := store.Open(context.Background(), scope, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(f); err == nil {
		t.Fatal("truncated response accepted")
	}
	f.Close()
	mode.Store(0)
	f, err = store.Open(context.Background(), scope, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Seek(4, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Read(make([]byte, 3)); err == nil {
		t.Fatal("ignored Range accepted")
	}
	f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	f, err = store.Open(ctx, scope, key)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = f.Read(make([]byte, 1)); err == nil {
		t.Fatal("read ignored cancellation")
	}
	f.Close()
}
