package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

type object = map[string]any
type configuration struct {
	Version        string `json:"version"`
	GeneratorImage string `json:"generatorImage"`
	GoModule       string `json:"goModule"`
	PythonPackage  string `json:"pythonPackage"`
	NPMPackage     string `json:"npmPackage"`
}

func (c configuration) validate() error {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$`).MatchString(c.Version) {
		return fmt.Errorf("invalid release version %q", c.Version)
	}
	if c.GeneratorImage == "" || c.GoModule == "" || c.PythonPackage == "" || c.NPMPackage == "" {
		return fmt.Errorf("incomplete client configuration")
	}
	return nil
}

type tool struct {
	root             string
	config           configuration
	out, diagnostics io.Writer
}

func (t *tool) path(name string) string { return filepath.Join(t.root, filepath.FromSlash(name)) }
func envWith(values map[string]string) []string {
	entries := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, _ := strings.Cut(entry, "=")
		entries[k] = v
	}
	for k, v := range values {
		entries[k] = v
	}
	keys := sortedKeys(entries)
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		result = append(result, k+"="+entries[k])
	}
	return result
}
func (t *tool) command(ctx context.Context, dir string, env map[string]string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = envWith(env)
	cmd.Stdout = t.out
	cmd.Stderr = t.diagnostics
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
func (t *tool) run(ctx context.Context, dir string, env map[string]string, args ...string) error {
	if err := t.command(ctx, dir, env, args...).Run(); err != nil {
		return fmt.Errorf("%s in %s: %w", args[0], dir, err)
	}
	return nil
}
func (t *tool) output(ctx context.Context, dir string, env map[string]string, args ...string) (string, error) {
	cmd := t.command(ctx, dir, env, args...)
	var out, diagnostics bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", args[0], err, strings.TrimSpace(diagnostics.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%s: trailing JSON data", path)
	}
	return nil
}
func jsonBytes(value any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	err := e.Encode(value)
	return b.Bytes(), err
}
func writeJSON(path string, value any) error {
	data, err := jsonBytes(value)
	if err != nil {
		return err
	}
	return writeFile(path, data, 0644)
}
func writeFile(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".wave-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFile(dst, data, info.Mode().Perm())
}
func ignoredPart(name string) bool {
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == "__pycache__" || part == ".pytest_cache" {
			return true
		}
	}
	return false
}
func treeFiles(path string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(path, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(path, name)
		if ignoredPart(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink: %s", name)
		}
		if !d.IsDir() {
			names = append(names, name)
		}
		return nil
	})
	sort.Strings(names)
	return names, err
}
func copyTree(src, dst string) error {
	names, err := treeFiles(src)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	for _, name := range names {
		rel, _ := filepath.Rel(src, name)
		if err = copyFile(name, filepath.Join(dst, rel)); err != nil {
			return err
		}
	}
	return nil
}
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func snapshot(path string) (map[string]string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	names := []string{path}
	if info.IsDir() {
		names, err = treeFiles(path)
		if err != nil {
			return nil, err
		}
	}
	result := map[string]string{}
	for _, name := range names {
		rel, _ := filepath.Rel(path, name)
		sum, err := hashFile(name)
		if err != nil {
			return nil, err
		}
		result[rel] = sum
	}
	return result, nil
}
func samePath(a, b string) (bool, error) {
	left, err := snapshot(a)
	if err != nil {
		return false, err
	}
	right, err := snapshot(b)
	return reflect.DeepEqual(left, right), err
}

// Copy before touching the destination. Restore its previous contents if rename fails.
func replacePath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	parent := filepath.Dir(dst)
	if err = os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".wave-generated-*")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()
	next := filepath.Join(stage, "next")
	if err = copyTree(src, next); err != nil {
		return err
	}
	old := filepath.Join(stage, "old")
	existed := false
	if _, err = os.Stat(dst); err == nil {
		if err = os.Rename(dst, old); err != nil {
			return err
		}
		existed = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Rename(next, dst); err != nil {
		if existed {
			if restore := os.Rename(old, dst); restore != nil {
				cleanup = false
				return fmt.Errorf("%v; restore failed (backup retained at %s): %w", err, old, restore)
			}
		}
		return err
	}
	return nil
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func obj(value any) object { m, _ := value.(map[string]any); return m }
func str(value any) string { s, _ := value.(string); return s }
func list(value any) []any { items, _ := value.([]any); return items }
func at(value any, keys ...string) any {
	for _, key := range keys {
		value = obj(value)[key]
	}
	return value
}
