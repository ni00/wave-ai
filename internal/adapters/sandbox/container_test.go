package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"gorm.io/gorm"
	"wave-ai.local/wave/internal/platform/xid"
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

// frame writes one stdcopy frame: stream type byte, zero padding, and a
// big-endian payload length.
func frame(w io.Writer, stream byte, data []byte) {
	h := []byte{stream, 0, 0, 0, byte(len(data) >> 24), byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data))}
	w.Write(append(h, data...))
}

// engineStub serves the engine endpoints Ensure and ListDir touch for an
// already-provisioned running sandbox: engine info, container inspection and
// the exec trio used by listings. It pins API version 1.40 like the probe
// test above.
type engineStub struct {
	server  *httptest.Server
	sid     string
	execOut string
	execErr string
	execCd  int
	execCmd []string
}

func (e *engineStub) inspectJSON() string {
	vol := strings.ToLower("wave-" + e.sid)
	return fmt.Sprintf(`{"Id":"abc123","State":{"Status":"running","Running":true},"Config":{"Image":"test-image","Labels":{"wave.managed":"true","wave.session":%q,"wave.backend":"gvisor","wave.role":"sandbox"}},"HostConfig":{"Runtime":"wave-runsc","NetworkMode":"none","Memory":1073741824,"MemorySwap":1073741824,"CpuPeriod":100000,"CpuQuota":100000,"PidsLimit":256,"Privileged":false,"SecurityOpt":["no-new-privileges:true"],"Devices":[]},"Mounts":[{"Type":"volume","Name":"%s-uploads","Destination":"/mnt/session/uploads","RW":false},{"Type":"volume","Name":"%s-skills","Destination":"/mnt/skills","RW":false}]}`, e.sid, vol, vol)
}

func newEngineStub(t *testing.T, sid string) *engineStub {
	t.Helper()
	e := &engineStub{sid: sid}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.40/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"Runtimes":{"wave-runsc":{"path":"/usr/local/bin/runsc","runtimeArgs":["--platform=systrap","--overlay2=none","--file-access=shared"]}}}`)
	})
	mux.HandleFunc("/v1.40/containers/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1.40/containers/")
		switch {
		case r.Method == "GET" && strings.HasSuffix(rest, "/json"):
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, e.inspectJSON())
		case r.Method == "POST" && strings.HasSuffix(rest, "/exec"):
			var body struct{ Cmd []string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("bad exec create body: %v", err)
			}
			e.execCmd = body.Cmd
			io.WriteString(w, `{"Id":"e1"}`)
		default:
			t.Errorf("unexpected engine operation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("/v1.40/exec/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1.40/exec/")
		switch {
		case r.Method == "POST" && rest == "e1/start":
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Errorf("server does not support hijacking")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack failed: %v", err)
				return
			}
			defer conn.Close()
			conn.Write([]byte("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"))
			frame(conn, 1, []byte(e.execOut))
			frame(conn, 2, []byte(e.execErr))
			// Half-close and drain instead of a plain Close: closing with
			// unread request bytes would RST the connection and destroy the
			// frames before the client consumes them.
			if tcp, ok := conn.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
				_ = tcp.SetReadDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.Copy(io.Discard, tcp)
			}
		case r.Method == "GET" && rest == "e1/json":
			fmt.Fprintf(w, `{"Id":"e1","Running":false,"ExitCode":%d}`, e.execCd)
		default:
			t.Errorf("unexpected exec operation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	e.server = httptest.NewServer(mux)
	t.Cleanup(e.server.Close)
	return e
}

func stubbedContainer(t *testing.T, e *engineStub, db *gorm.DB) *Container {
	t.Helper()
	c, err := NewContainer(ContainerOptions{Backend: "gvisor", Store: db, Image: "test-image", MaxRunning: 2, MemoryBudgetMiB: 2048})
	if err != nil {
		t.Fatal(err)
	}
	c.api.Close()
	c.api, err = client.New(client.WithHost("tcp://"+strings.TrimPrefix(e.server.URL, "http://")), client.WithAPIVersion("1.40"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func provisionedRecord(sid string) Record {
	return Record{SessionID: sid, Backend: "gvisor", EngineHost: "unix:///var/run/docker.sock", Runtime: "wave-runsc", Network: "none", StagerID: "stg", BackendID: "abc123", Name: "wave-" + sid, State: "running", CPUs: 1, MemoryMiB: 1024, Image: "test-image"}
}

func TestEnsureSkipsRedundantRowWrites(t *testing.T) {
	db, prefix := capacityDB(t)
	sid := prefix + "-skip"
	e := newEngineStub(t, sid)
	c := stubbedContainer(t, e, db)
	past := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	scenario := func(state, backendID string) {
		t.Helper()
		if err := db.Where("session_id = ?", sid).Delete(&Record{}).Error; err != nil {
			t.Fatal(err)
		}
		r := provisionedRecord(sid)
		r.State, r.BackendID = state, backendID
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&Record{}).Where("session_id = ?", sid).UpdateColumn("updated_at", past).Error; err != nil {
			t.Fatal(err)
		}
	}
	fetch := func() Record {
		t.Helper()
		var r Record
		if err := db.Where("session_id = ?", sid).Take(&r).Error; err != nil {
			t.Fatal(err)
		}
		return r
	}
	// A healthy, fully recorded sandbox performs no row writes at all.
	scenario("running", "abc123")
	if err := c.Ensure(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if r := fetch(); !r.UpdatedAt.Equal(past) {
		t.Fatalf("steady-state Ensure rewrote the record (updated_at moved to %v)", r.UpdatedAt)
	}
	// A crash between start and the terminal state write still converges.
	scenario("starting", "abc123")
	if err := c.Ensure(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if r := fetch(); r.State != "running" || r.UpdatedAt.Equal(past) {
		t.Fatalf("stale starting state not converged: %+v", r)
	}
	// A missing backend identity is persisted when the engine reports one.
	scenario("running", "")
	if err := c.Ensure(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if r := fetch(); r.BackendID != "abc123" || r.UpdatedAt.Equal(past) {
		t.Fatalf("backend identity not persisted: %+v", r)
	}
}

func TestListDirExecutesOneLevelFind(t *testing.T) {
	db, prefix := capacityDB(t)
	sid := prefix + "-list"
	e := newEngineStub(t, sid)
	c := stubbedContainer(t, e, db)
	r := provisionedRecord(sid)
	if err := db.Create(&r).Error; err != nil {
		t.Fatal(err)
	}
	e.execOut = "d\x00\x00f\x00report final.txt\x00d\x00sub dir\x00l\x00dangling\x00"
	names, err := c.ListDir(context.Background(), sid, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"report final.txt", "sub dir/"}; !slices.Equal(names, want) {
		t.Fatalf("listing = %q, want %q", names, want)
	}
	// The path is exec'd as one argv element: no shell quoting is involved.
	if want := []string{"find", "/workspace", "-maxdepth", "1", "-printf", `%y\0%P\0`}; !slices.Equal(e.execCmd, want) {
		t.Fatalf("exec argv = %q, want %q", e.execCmd, want)
	}
	// Engine-side failures surface with the exit code and stderr.
	e.execOut, e.execErr, e.execCd = "", "find: '/gone': No such file or directory", 1
	if _, err = c.ListDir(context.Background(), sid, "/gone"); err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("missing path listing error = %v", err)
	}
}

func TestParseFindListing(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []string
		fail bool
	}{
		{name: "empty directory", out: "d\x00\x00", want: []string{}},
		{name: "entries keep type suffix", out: "d\x00\x00f\x00a.txt\x00d\x00sub\x00", want: []string{"a.txt", "sub/"}},
		{name: "spaces and newlines in names", out: "d\x00\x00f\x00hello world.txt\x00d\x00new\nline\x00", want: []string{"hello world.txt", "new\nline/"}},
		{name: "non regular entries are skipped", out: "d\x00\x00l\x00link\x00f\x00ok\x00", want: []string{"ok"}},
		{name: "root must be a directory", out: "f\x00\x00", fail: true},
		{name: "unterminated stream", out: "d\x00\x00f\x00name", fail: true},
		{name: "odd field count", out: "d\x00\x00f", fail: true},
		{name: "empty stream", out: "", fail: true},
		{name: "corrupted type field", out: "d\x00\x00ff\x00name\x00", fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFindListing(tc.out)
			if tc.fail {
				if err == nil {
					t.Fatalf("parse(%q) = %q, want error", tc.out, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("parse(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// TestListDirAgainstRealEngine exercises the listing pipeline (argv passing,
// stdcopy framing, GNU find -printf semantics, parsing) against a real local
// engine. The container deliberately runs without gVisor: the semantics under
// test do not depend on the sandbox runtime. Set WAVE_TEST_DOCKER=1 to enable;
// WAVE_TEST_DOCKER_IMAGE overrides the image (needs GNU findutils, as any
// Debian-based image does).
func TestListDirAgainstRealEngine(t *testing.T) {
	if os.Getenv("WAVE_TEST_DOCKER") == "" {
		t.Skip("set WAVE_TEST_DOCKER=1 with a local engine to exercise real-container listing")
	}
	image := os.Getenv("WAVE_TEST_DOCKER_IMAGE")
	if image == "" {
		image = DefaultContainerImage
	}
	c, err := NewContainer(ContainerOptions{Backend: "gvisor"})
	if err != nil {
		t.Fatal(err)
	}
	c.api.Close()
	if c.api, err = client.New(client.WithHost("unix:///var/run/docker.sock")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{Name: "wave-listdir-" + xid.New("verify"), Image: image, HostConfig: &container.HostConfig{}, Config: &container.Config{Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"}}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	t.Cleanup(func() { c.api.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true}) })
	if _, err = c.api.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"mkdir", "-p", "/workspace/sub dir"},
		// The newline filename is produced via $(printf ...) so no literal
		// newline has to survive Go source quoting.
		{"sh", "-c", `printf hi > "/workspace/report final.txt"; printf x > "$(printf '/workspace/new\nline.txt')"; ln -s "/workspace/report final.txt" /workspace/link`},
	} {
		if _, _, code, err := c.execCapture(ctx, id, argv); err != nil || code != 0 {
			t.Fatalf("setup %q: exit %d, err %v", argv, code, err)
		}
	}
	stdout, stderr, code, err := c.execCapture(ctx, id, []string{"find", "/workspace", "-maxdepth", "1", "-printf", `%y\0%P\0`})
	if err != nil || code != 0 {
		t.Fatalf("find: exit %d, err %v, stderr %q", code, err, stderr)
	}
	got, err := parseFindListing(stdout)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"new\nline.txt", "report final.txt", "sub dir/"}
	if !slices.Equal(got, want) {
		t.Fatalf("listing = %q, want %q", got, want)
	}
	// Cross-check the find listing against the tar listing it replaced.
	result, err := c.api.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Content.Close()
	tarNames := []string{}
	if err = walkArchive(result.Content, "workspace", 128<<20, func(rel string, h *tar.Header, _ io.Reader) error {
		if rel != "" && !strings.Contains(rel, "/") {
			if h.Typeflag == tar.TypeDir {
				rel += "/"
			}
			tarNames = append(tarNames, rel)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(tarNames)
	if !slices.Equal(got, tarNames) {
		t.Fatalf("find listing %q disagrees with tar listing %q", got, tarNames)
	}
	// A non-directory path reports the same error the tar listing produced.
	stdout, _, code, err = c.execCapture(ctx, id, []string{"find", "/workspace/report final.txt", "-maxdepth", "1", "-printf", `%y\0%P\0`})
	if err != nil || code != 0 {
		t.Fatalf("find on file: exit %d, err %v", code, err)
	}
	if _, err = parseFindListing(stdout); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file listing error = %v, want not-a-directory", err)
	}
}

// Stop must release the capacity reservation when the backing container is
// already gone (external cleanup, host restarts, pruned containers): the
// sandbox is de facto stopped, and returning the inspect error would pin the
// per-host sandbox slots until manual cleanup.
func TestStopReleasesMissingContainer(t *testing.T) {
	db, prefix := capacityDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected engine operation: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	c, err := NewContainer(ContainerOptions{Backend: "gvisor", Store: db, CPUs: 1, MemoryMiB: 1024, Image: "test-image", MaxRunning: 2, MemoryBudgetMiB: 3072})
	if err != nil {
		t.Fatal(err)
	}
	c.api.Close()
	c.api, err = client.New(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithAPIVersion("1.40"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sid := prefix + "-gone"
	if err := db.Create(&Record{SessionID: sid, Name: "wave-" + sid, State: "running", BackendID: "missing-container", Backend: "gvisor", EngineHost: "unix:///var/run/docker.sock", Runtime: "wave-runsc", Network: "none", CPUs: 1, MemoryMiB: 1024, Image: "test-image"}).Error; err != nil {
		t.Fatal(err)
	}
	if err = c.Stop(context.Background(), sid); err != nil {
		t.Fatalf("stop with missing container: %v", err)
	}
	var r Record
	if err := db.Take(&r, "session_id = ?", sid).Error; err != nil {
		t.Fatal(err)
	}
	if r.State != "stopped" {
		t.Fatalf("record state = %q, want stopped", r.State)
	}
}
