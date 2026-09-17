package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/moby/moby/client"
)

func TestEngineProbeNeverFallsBack(t *testing.T) {
	for _, backend := range []string{"gvisor"} {
		t.Run(backend, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1.40/info" {
					t.Errorf("unexpected engine operation: %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"CgroupVersion":"2","Runtimes":{"runc":{"path":"runc"}},"SecurityOptions":["name=seccomp"]}`)
			}))
			defer server.Close()
			c, err := NewContainer(ContainerOptions{Backend: backend})
			if err != nil {
				t.Fatal(err)
			}
			c.api.Close()
			c.api, err = client.New(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithAPIVersion("1.40"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.Ensure(context.Background(), "unsafe-host"); err == nil {
				t.Fatal("unsafe engine accepted")
			}
		})
	}
}

func TestCaptureTextAcceptsBinaryAndKeepsBound(t *testing.T) {
	raw := strings.Repeat("\x00\xff", maxCaptureBytes)
	text := captureText(raw)
	if strings.ContainsRune(text, 0) || !utf8.ValidString(text) || len(text) > maxCaptureBytes+64 || !strings.Contains(text, "truncated") {
		t.Fatalf("invalid capture, bytes=%d", len(text))
	}
}

func TestContainerArchiveBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		headers []*tar.Header
		fail    bool
		calls   int
	}{
		{"regular", []*tar.Header{{Name: "outputs", Typeflag: tar.TypeDir}, {Name: "outputs/a", Typeflag: tar.TypeReg, Size: 2}}, false, 2},
		{"traversal", []*tar.Header{{Name: "outputs/../secret", Typeflag: tar.TypeReg}}, true, 0},
		{"absolute", []*tar.Header{{Name: "/outputs/secret", Typeflag: tar.TypeReg}}, true, 0},
		{"different root", []*tar.Header{{Name: "secret", Typeflag: tar.TypeReg}}, true, 0},
		{"symlink skipped", []*tar.Header{{Name: "outputs/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow"}}, false, 0},
		{"hardlink skipped", []*tar.Header{{Name: "outputs/link", Typeflag: tar.TypeLink, Linkname: "outputs/a"}}, false, 0},
		{"oversize", []*tar.Header{{Name: "outputs/a", Typeflag: tar.TypeReg, Size: 17}}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := tar.NewWriter(&buf)
			for _, h := range tc.headers {
				if err := w.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					_, _ = w.Write(make([]byte, h.Size))
				}
			}
			_ = w.Close()
			calls := 0
			err := walkArchive(&buf, "outputs", 16, func(_ string, _ *tar.Header, r io.Reader) error { calls++; _, e := io.Copy(io.Discard, r); return e })
			if (err != nil) != tc.fail || calls != tc.calls {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestContainerRejectsUnsafeConfiguration(t *testing.T) {
	for _, o := range []ContainerOptions{{Backend: "runc"}, {Backend: "gvisor", Host: "tcp://127.0.0.1:2375"}, {Backend: "gvisor", Network: "host"}, {Backend: "gvisor", Network: "container:other"}} {
		if c, err := NewContainer(o); err == nil {
			c.Close()
			t.Fatalf("accepted %+v", o)
		}
	}
}

func TestPublicationStopsAtFirstRejectedFile(t *testing.T) {
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	for _, name := range []string{"outputs/a", "outputs/b"} {
		if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rejected := errors.New("upload failed")
	err := walkArchive(&archive, "outputs", 100, func(string, *tar.Header, io.Reader) error {
		calls++
		return rejected
	})
	if !errors.Is(err, rejected) || calls != 1 {
		t.Fatal("publication continued after failure", err, calls)
	}
}
