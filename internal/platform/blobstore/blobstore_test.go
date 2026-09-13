package blobstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func exerciseStore(t *testing.T, store *Store) string {
	t.Helper()
	ctx := context.Background()
	owner := Scope{OrgID: "org-a", OwnerID: "alice"}
	data := []byte("0123456789abcdef")
	key, size, err := store.PutBytes(ctx, owner, data)
	if err != nil || size != int64(len(data)) {
		t.Fatalf("put: %d %v", size, err)
	}
	again, _, err := store.PutBytes(ctx, owner, data)
	if err != nil || again != key {
		t.Fatalf("dedup: %s %v", again, err)
	}
	for _, peer := range []Scope{{"org-a", "bob"}, {"org-b", "alice"}} {
		if _, err := store.Open(ctx, peer, key); err == nil {
			t.Fatal("cross-owner object read allowed")
		}
		peerKey, _, err := store.PutBytes(ctx, peer, data)
		if err != nil || peerKey == key {
			t.Fatalf("cross-owner dedup: %s %v", peerKey, err)
		}
	}
	for _, bad := range []string{"", "../secret", key + "/../secret", strings.ToUpper(key)} {
		if _, err := store.Open(ctx, owner, bad); err == nil {
			t.Fatalf("invalid key accepted: %q", bad)
		}
	}
	if _, _, err := store.PutBytes(ctx, Scope{"../escape", "alice"}, data); err == nil {
		t.Fatal("invalid scope accepted")
	}
	got, err := store.ReadAll(ctx, owner, key, 16)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read: %q %v", got, err)
	}
	if _, err := store.ReadAll(ctx, owner, key, 15); err == nil {
		t.Fatal("read limit ignored")
	}
	f, err := store.Open(ctx, owner, key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, tc := range []struct {
		offset int64
		whence int
		want   string
	}{{4, io.SeekStart, "456"}, {-3, io.SeekEnd, "def"}, {0, io.SeekStart, "012"}, {0, io.SeekCurrent, "345"}} {
		if _, err := f.Seek(tc.offset, tc.whence); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 3)
		if _, err := io.ReadFull(f, b); err != nil || string(b) != tc.want {
			t.Fatalf("seek/read: %q %v", b, err)
		}
	}
	if _, err := f.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek accepted")
	}
	// Exercise net/http's full, suffix, multi-range and conditional responses.
	for _, tc := range []struct {
		rangeHeader string
		status      int
		body        string
	}{{"", 200, string(data)}, {"bytes=3-6", 206, "3456"}, {"bytes=-3", 206, "def"}, {"bytes=99-", 416, ""}, {"bytes=0-1,14-15", 206, ""}} {
		r := httptest.NewRequest("GET", "/content", nil)
		r.Header.Set("Range", tc.rangeHeader)
		w := httptest.NewRecorder()
		f, err := store.Open(ctx, owner, key)
		if err != nil {
			t.Fatal(err)
		}
		http.ServeContent(w, r, "file.txt", time.Unix(1, 0), f)
		f.Close()
		if w.Code != tc.status || (tc.body != "" && w.Body.String() != tc.body) {
			t.Fatalf("range %s: %d %q", tc.rangeHeader, w.Code, w.Body.String())
		}
	}
	empty, _, err := store.PutBytes(ctx, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := store.ReadAll(ctx, owner, empty, 1); err != nil || len(b) != 0 {
		t.Fatalf("empty: %q %v", b, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Open(canceled, owner, key); err == nil {
		t.Fatal("canceled read allowed")
	}
	if _, _, err := store.PutBytes(canceled, owner, data); err == nil {
		t.Fatal("canceled write allowed")
	}
	return key
}

func TestLocalStore(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := exerciseStore(t, store)
	ctx := context.Background()
	scope := Scope{"org-a", "alice"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, _, err := store.PutBytes(ctx, scope, []byte("concurrent")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := os.WriteFile(filepath.Join(dir, "blobs", key), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadAll(ctx, scope, key, 100); err == nil {
		t.Fatal("corrupted object accepted")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Remove(filepath.Join(dir, "blobs", key))
	if err := os.Symlink(outside, filepath.Join(dir, "blobs", key)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ctx, scope, key); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

// This test uses an isolated real S3-compatible server started by make integration.
func TestS3Integration(t *testing.T) {
	endpoint := os.Getenv("WAVE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("isolated S3 endpoint required")
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "wave-test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wave-test-password")
	t.Setenv("AWS_SESSION_TOKEN", "")
	bucket := fmt.Sprintf("wave-test-%d", time.Now().UnixNano())
	store, err := NewS3(context.Background(), S3Options{Bucket: bucket, Region: "us-east-1", Endpoint: endpoint, Prefix: "test/blobs", PathStyle: true, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	b := store.backend.(*s3Backend)
	if _, err = b.client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		rows, err := b.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err != nil {
			t.Error(err)
			return
		}
		for _, o := range rows.Contents {
			if _, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: o.Key}); err != nil {
				t.Error(err)
			}
		}
		if _, err := b.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
			t.Error(err)
		}
	})
	key := exerciseStore(t, store)
	// SeaweedFS splits large objects into chunks; reads and seeks must cross
	// chunk boundaries without returning gaps or duplicated bytes.
	ctx := context.Background()
	scope := Scope{OrgID: "org-a", OwnerID: "alice"}
	large := make([]byte, 9<<20)
	for i := range large {
		large[i] = byte((i*31 + i/(1<<20)) % 251)
	}
	largeKey, _, err := store.PutBytes(ctx, scope, large)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadAll(ctx, scope, largeKey, int64(len(large)))
	if err != nil || !bytes.Equal(got, large) {
		t.Fatalf("large object round trip: %v", err)
	}
	f, err := store.Open(ctx, scope, largeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, offset := range []int64{(4 << 20) - 17, (8 << 20) - 17, 1 << 20} {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 1024)
		if _, err := io.ReadFull(f, got); err != nil || !bytes.Equal(got, large[offset:offset+1024]) {
			t.Fatalf("large object range at %d: %v", offset, err)
		}
	}
	res, err := http.Get(endpoint + "/" + bucket + "/test/blobs/" + key)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous object access: %d", res.StatusCode)
	}
	// A configured prefix is applied to every object; it is not exposed in file IDs.
	rows, err := b.client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range rows.Contents {
		if !strings.HasPrefix(aws.ToString(o.Key), "test/blobs/org-") {
			t.Fatalf("unexpected object key: %s", aws.ToString(o.Key))
		}
	}
}

func TestS3Options(t *testing.T) {
	for _, o := range []S3Options{
		{Bucket: ""}, {Bucket: "valid-bucket", Prefix: "../other"},
		{Bucket: "valid-bucket", Endpoint: "http://localhost:9000"},
		{Bucket: "valid-bucket", Endpoint: "https://user:password@example.com"},
		{Bucket: "valid-bucket", Endpoint: "https://example.com/bucket"},
	} {
		if o.Validate() == nil {
			t.Fatalf("invalid options accepted: %+v", o)
		}
	}
	if err := (S3Options{Bucket: "valid-bucket", Endpoint: "http://localhost:9000", AllowHTTP: true, Prefix: "wave/files"}).Validate(); err != nil {
		t.Fatal(err)
	}
}
