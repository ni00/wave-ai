package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := writeFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedCheckAndReplacement(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	tool := &tool{root: root, out: io.Discard, diagnostics: io.Discard}
	for _, name := range generatedPaths {
		put(t, filepath.Join(stage, name), "new")
		put(t, filepath.Join(root, name), "old")
	}
	// Directory replacement must drop obsolete generated files, preserving siblings.
	name := "sdks/go/generated"
	if err := os.Remove(filepath.Join(stage, name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(stage, name, "current.go"), "generated")
	put(t, filepath.Join(root, name, "obsolete.go"), "old")
	put(t, filepath.Join(root, "sdks/go/client.go"), "handwritten")
	before, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = tool.publishGenerated(stage, true); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("check returned %v", err)
	}
	after, err := snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("check changed the checkout")
	}
	if err = tool.publishGenerated(stage, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, name, "obsolete.go")); !os.IsNotExist(err) {
		t.Fatal("obsolete generated file survived")
	}
	data, err := os.ReadFile(filepath.Join(root, "sdks/go/client.go"))
	if err != nil || string(data) != "handwritten" {
		t.Fatal("handwritten runtime changed")
	}
	if err = tool.publishGenerated(stage, true); err != nil {
		t.Fatal("second generation is not stable:", err)
	}
	// An incomplete stage must fail before replacing any earlier output.
	put(t, filepath.Join(stage, generatedPaths[0]), "partial")
	if err = os.Remove(filepath.Join(stage, generatedPaths[len(generatedPaths)-1])); err != nil {
		t.Fatal(err)
	}
	before, err = snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = tool.publishGenerated(stage, false); err == nil {
		t.Fatal("accepted an incomplete stage")
	}
	after, err = snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("incomplete generation partially changed the checkout")
	}
}

func TestFailedDirectoryReplacementPreservesOutput(t *testing.T) {
	root := t.TempDir()
	source, dest := filepath.Join(root, "stage"), filepath.Join(root, "release")
	put(t, filepath.Join(source, "good.txt"), "new")
	put(t, filepath.Join(dest, "old.txt"), "existing release")
	if err := os.Symlink(filepath.Join(source, "good.txt"), filepath.Join(source, "link")); err != nil {
		t.Skip(err)
	}
	before, err := snapshot(dest)
	if err != nil {
		t.Fatal(err)
	}
	if err = replacePath(source, dest); err == nil {
		t.Fatal("accepted a symlink in staged output")
	}
	after, err := snapshot(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed staging destroyed existing release")
	}
}

func TestOperationMetadata(t *testing.T) {
	spec := object{"security": []any{object{"BearerAuth": []any{}}}, "paths": object{"/v1/items": object{"get": object{"operationId": "listItems", "summary": "List items", "responses": object{}}, "post": object{"operationId": "createItem", "security": []any{}, "summary": "Create item", "responses": object{}}}}}
	metadata := object{"listItems": object{"command": "items list", "pagination": "offset"}, "createItem": object{"command": "items create", "idempotent": true}}
	ops, order, err := operationMap(spec, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"listItems", "createItem"}) || at(ops, "listItems", "authenticated") != true || at(ops, "createItem", "authenticated") != false || at(ops, "listItems", "pagination") != "offset" {
		t.Fatalf("operation semantics lost: %#v", ops)
	}
	delete(metadata, "createItem")
	if _, _, err = operationMap(spec, metadata); err == nil {
		t.Fatal("accepted missing metadata")
	}
	metadata["createItem"] = object{"command": "items list"}
	if _, _, err = operationMap(spec, metadata); err == nil {
		t.Fatal("accepted duplicate command")
	}
	metadata["createItem"] = object{"command": "items create"}
	metadata["removed"] = object{"command": "items removed"}
	if _, _, err = operationMap(spec, metadata); err == nil {
		t.Fatal("accepted stale metadata")
	}
	delete(metadata, "removed")
	obj(at(spec, "paths", "/v1/items", "post"))["operationId"] = "listItems"
	if _, _, err = operationMap(spec, metadata); err == nil {
		t.Fatal("accepted duplicate operation ID")
	}
}

func TestBilingualContractPreservesLiteralValues(t *testing.T) {
	a := object{"description": "English", "schema": object{"description": "Field", "example": object{"description": "literal"}, "default": object{"summary": "value"}}}
	b := object{"description": "中文", "schema": object{"description": "字段", "example": object{"description": "literal"}, "default": object{"summary": "value"}}}
	if !reflect.DeepEqual(withoutDocumentation(a), withoutDocumentation(b)) {
		t.Fatal("documentation translation changed the contract")
	}
	obj(at(b, "schema", "example"))["description"] = "changed"
	if reflect.DeepEqual(withoutDocumentation(a), withoutDocumentation(b)) {
		t.Fatal("contract check ignored a changed example")
	}
}

func TestArchivesAndChecksums(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "binary")
	content := "wave\x00\n中文"
	put(t, source, content)
	for _, ext := range []string{".zip", ".tar.gz"} {
		t.Run(ext, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "client"+ext)
			if err := writeArchive(archive, []archiveFile{{source, "bin/wavectl"}}); err != nil {
				t.Fatal(err)
			}
			data, err := archiveMember(archive, "bin/wavectl")
			if err != nil || string(data) != content {
				t.Fatalf("archive content: %q, %v", data, err)
			}
			if _, err = archiveMember(archive, "missing"); err == nil {
				t.Fatal("accepted missing member")
			}
			if err = writeArchive(archive, []archiveFile{{source, "../outside"}}); err == nil {
				t.Fatal("accepted traversal entry")
			}
		})
	}
	if err := writeChecksums(root); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksums(root); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "extra"), "unlisted")
	if err := verifyChecksums(root); err == nil {
		t.Fatal("accepted unlisted artifact")
	}
	if err := os.Remove(filepath.Join(root, "extra")); err != nil {
		t.Fatal(err)
	}
	put(t, source, "tampered")
	if err := verifyChecksums(root); err == nil {
		t.Fatal("accepted modified artifact")
	}
	put(t, filepath.Join(root, "SHA256SUMS"), strings.Repeat("0", 64)+"  ../outside\n")
	if err := verifyChecksums(root); err == nil {
		t.Fatal("accepted checksum traversal")
	}
}

func TestCanceledCommandDoesNotStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := &tool{root: t.TempDir(), out: io.Discard, diagnostics: io.Discard}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = tool.run(ctx, tool.root, nil, executable, "-test.run=^$"); err == nil {
		t.Fatal("canceled command succeeded")
	}
}

func TestInvalidInvocationDoesNotNeedCheckout(t *testing.T) {
	var output bytes.Buffer
	if err := execute(context.Background(), []string{"--help"}, &output, io.Discard); err != nil || !strings.Contains(output.String(), "compat SPEC OPERATIONS") {
		t.Fatalf("help: %v %s", err, output.String())
	}
	for _, args := range [][]string{{"unknown"}, {"generate", "--invalid"}, {"check", "extra"}, {"compat", "only-one"}} {
		if err := execute(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
