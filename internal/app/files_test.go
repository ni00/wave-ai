package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gorm.io/gorm/clause"

	"wave-ai.local/wave/internal/modules/files"
	"wave-ai.local/wave/internal/modules/skills"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/blobstore"
	"wave-ai.local/wave/internal/platform/config"
	"wave-ai.local/wave/internal/platform/xid"
)

func storageTestConfig(t *testing.T, dsn, backend string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DatabaseURL: dsn, DataDir: dir, SandboxBackend: "local", SandboxLocalRoot: dir + "/work", StorageBackend: backend}
	if backend != "s3" {
		return cfg
	}
	cfg.MasterKey = bytes.Repeat([]byte{42}, 32)
	cfg.DataDir = dir + "/unused"
	endpoint := os.Getenv("WAVE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("isolated S3 endpoint required")
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "wave-test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wave-test-password")
	t.Setenv("AWS_SESSION_TOKEN", "")
	bucket := fmt.Sprintf("wave-app-%d", time.Now().UnixNano())
	cfg.S3 = blobstore.S3Options{Bucket: bucket, Region: "us-east-1", Endpoint: endpoint, Prefix: "wave", PathStyle: true, AllowHTTP: true}
	sdk, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"))
	if err != nil {
		t.Fatal(err)
	}
	client := s3.NewFromConfig(sdk, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rows, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err != nil {
			t.Error(err)
			return
		}
		for _, o := range rows.Contents {
			if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: o.Key}); err != nil {
				t.Error(err)
			}
		}
		if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
			t.Error(err)
		}
	})
	return cfg
}

func TestFileStorageAuthorization(t *testing.T) {
	for _, backend := range []string{"local", "s3"} {
		t.Run(backend, func(t *testing.T) { testFileStorageAuthorization(t, backend) })
	}
}
func testFileStorageAuthorization(t *testing.T, backend string) {
	dsn := os.Getenv("WAVE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	if err := Init(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	cfg := storageTestConfig(t, dsn, backend)
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	cfg2 := *cfg
	if backend == "s3" {
		cfg2.DataDir = t.TempDir() + "/unused"
	}
	b, err := New(ctx, &cfg2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	if backend == "s3" {
		sealed, nonce, err := a.Box.Seal([]byte("shared vault secret"))
		if err != nil {
			t.Fatal(err)
		}
		opened, err := b.Box.Open(sealed, nonce)
		if err != nil || string(opened) != "shared vault secret" {
			t.Fatalf("independent S3 replicas cannot share encrypted credentials: %v", err)
		}
	}
	server := httptest.NewServer(b.Handler())
	t.Cleanup(server.Close)
	org := xid.New("org")
	newKey := func(org, user string) string {
		k, e := auth.Bootstrap(ctx, a.DB, org, user, "files", auth.ScopeAPI)
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	owner := newKey(org, "alice")
	peers := []string{newKey(org, "bob"), newKey(xid.New("org"), "alice")}
	baseURL := server.URL
	call := func(key, method, path, contentType string, body []byte, rangeHeader string, status int) ([]byte, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != status {
			t.Fatalf("%s %s = %d: %s", method, path, res.StatusCode, data)
		}
		return data, res.Header
	}
	upload := func(key, path, name string, data []byte) string {
		t.Helper()
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		f, e := w.CreateFormFile("file", name)
		if e != nil {
			t.Fatal(e)
		}
		f.Write(data)
		w.Close()
		raw, _ := call(key, "POST", path, w.FormDataContentType(), body.Bytes(), "", 201)
		var row map[string]any
		if e := json.Unmarshal(raw, &row); e != nil {
			t.Fatal(e)
		}
		if _, ok := row["blob_key"]; ok {
			t.Fatal("blob key exposed")
		}
		return row["id"].(string)
	}
	// Upload to one API and download from another with an independent local directory.
	// The shared database plus S3 is sufficient for file and skill access.
	first := httptest.NewServer(a.Handler())
	defer first.Close()
	baseURL = first.URL
	payload := []byte("0123456789abcdef")
	id := upload(owner, "/v1/files", "report.html", payload)
	baseURL = server.URL
	path := "/v1/files/" + id
	data, headers := call(owner, "GET", path+"/content", "", nil, "", 200)
	if !bytes.Equal(data, payload) || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") || headers.Get("X-Content-Type-Options") != "nosniff" || headers.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unsafe or incorrect download: %q %v", data, headers)
	}
	data, _ = call(owner, "GET", path+"/content", "", nil, "bytes=4-7", 206)
	if string(data) != "4567" {
		t.Fatalf("wrong range: %q", data)
	}
	call("", "GET", path+"/content", "", nil, "", 401)
	call(newKey(org, "alice"), "GET", path+"/content", "", nil, "", 200)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	entry, _ := zw.Create("SKILL.md")
	entry.Write([]byte("---\nname: test\ndescription: Test skill metadata\n---\ntest skill"))
	zw.Close()
	skillID := upload(owner, "/v1/skills", "skill.zip", archive.Bytes())
	data, _ = call(owner, "GET", "/v1/skills/"+skillID+"/content", "", nil, "", 200)
	if !bytes.Equal(data, archive.Bytes()) {
		t.Fatal("skill download changed")
	}
	p, err := auth.Authenticate(ctx, a.DB, owner)
	if err != nil {
		t.Fatal(err)
	}
	loadedSkill, items, err := skills.Load(ctx, a.DB, a.Blobs, p, skillID)
	if loadedSkill.Name != "test" || loadedSkill.Description != "Test skill metadata" {
		t.Fatal("skill metadata lost", loadedSkill)
	}
	if err != nil || string(items["SKILL.md"]) != "---\nname: test\ndescription: Test skill metadata\n---\ntest skill" {
		t.Fatalf("skill staging: %v", err)
	}
	original, err := files.Get(ctx, a.DB, p, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range peers {
		call(peer, "GET", path, "", nil, "", 404)
		call(peer, "GET", path+"/content", "", nil, "", 404)
		call(peer, "DELETE", path, "", nil, "", 404)
		call(peer, "GET", "/v1/skills/"+skillID+"/content", "", nil, "", 404)
		raw, _ := call(peer, "GET", "/v1/files", "", nil, "", 200)
		if bytes.Contains(raw, []byte(id)) {
			t.Fatal("file leaked in list")
		}
		envRaw, _ := call(peer, "POST", "/v1/environments", "application/json", []byte(`{"name":"peer"}`), "", 201)
		var env map[string]any
		json.Unmarshal(envRaw, &env)
		body, _ := json.Marshal(map[string]any{"environment_id": env["id"], "file_ids": []string{id}})
		call(peer, "POST", "/v1/sessions", "application/json", body, "", 404)
		peerID := upload(peer, "/v1/files", "same.txt", payload)
		peerP, _ := auth.Authenticate(ctx, a.DB, peer)
		peerFile, e := files.Get(ctx, a.DB, peerP, peerID)
		if e != nil {
			t.Fatal(e)
		}
		if peerFile.BlobKey == original.BlobKey {
			t.Fatal("objects shared across owners")
		}
		// Even a mismatched metadata key must not open another owner's object.
		if e := a.DB.Model(&files.File{}).Where(clause.Eq{Column: "id", Value: peerID}).Update("blob_key", original.BlobKey).Error; e != nil {
			t.Fatal(e)
		}
		raw, _ = call(peer, "GET", "/v1/files/"+peerID+"/content", "", nil, "", 500)
		if bytes.Contains(raw, payload) {
			t.Fatal("mismatched metadata leaked content")
		}
	}
	artifact, err := files.PutArtifact(ctx, a.DB, a.Blobs, p, "", xid.New("task"), "nested/result.txt", []byte("artifact"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ = call(owner, "GET", "/v1/files/"+artifact.ID+"/content", "", nil, "", 200)
	if string(data) != "artifact" {
		t.Fatal("artifact lost")
	}
	call(owner, "DELETE", path, "", nil, "", 204)
	call(owner, "GET", path+"/content", "", nil, "", 404)
	// Previously admitted inputs remain readable internally after soft deletion.
	pinned, err := files.GetPinned(ctx, a.DB, p, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Blobs.ReadAll(ctx, blobstore.Scope{OrgID: p.OrgID, OwnerID: p.PrincipalID}, pinned.BlobKey, 32<<20); err != nil {
		t.Fatal(err)
	}
	if backend == "s3" {
		for _, dir := range []string{cfg.DataDir, cfg2.DataDir} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("S3 API created a local data directory")
			}
		}
	}
}

func TestS3RequiresMasterKey(t *testing.T) {
	for _, key := range [][]byte{nil, {1, 2, 3}} {
		_, err := New(context.Background(), &config.Config{StorageBackend: "s3", MasterKey: key})
		if err == nil || !strings.Contains(err.Error(), "WAVE_MASTER_KEY") {
			t.Fatalf("missing or invalid key should fail before opening database: %v", err)
		}
	}
}
