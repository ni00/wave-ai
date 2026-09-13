package command

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wave "github.com/ni00/wave-ai/sdks/go"
)

func invoke(t *testing.T, scenario, input string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("WAVE_CONFIG_FILE", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("WAVE_API_KEY", "wave-test-key")
	var out, err bytes.Buffer
	if scenario != "" {
		base := os.Getenv("WAVE_CLIENT_TEST_URL")
		if base == "" {
			t.Skip("run make clients-test")
		}
		args = append([]string{"--url", base + "/cli/" + scenario}, args...)
	}
	code := Execute(context.Background(), "test", args, strings.NewReader(input), &out, &err)
	return code, out.String(), err.String()
}
func TestSchemaCoverage(t *testing.T) {
	root := newRoot("test")
	for _, op := range wave.Operations() {
		cmd, remaining, e := root.Find(strings.Fields(op.Command))
		if e != nil || len(remaining) != 0 || cmd.RunE == nil {
			t.Fatal(op.Command, e, remaining)
		}
	}
	code, out, err := invoke(t, "", "", "schema")
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatal(code, out, err)
	}
}
func TestCLIContract(t *testing.T) {
	for _, scenario := range []string{"approve", "empty"} {
		args := []string{"tools", "reject", "call", "--task", "task"}
		if scenario == "empty" {
			args = []string{"tools", "result", "call", "--task", "task", "--result-file", "-"}
		}
		code, out, err := invoke(t, scenario, "", args...)
		if code != 0 || !json.Valid([]byte(out)) {
			t.Fatal(code, out, err)
		}
	}
	code, out, err := invoke(t, "error", "", "agents", "list")
	if code != 3 || out != "" || !strings.Contains(err, "req_contract") {
		t.Fatal(code, out, err)
	}
	code, out, err = invoke(t, "wait-action", "", "tasks", "wait", "task_target")
	if code != 6 || !strings.Contains(out, "required_actions") {
		t.Fatal(code, out, err)
	}
	code, out, err = invoke(t, "offset", "", "agents", "list", "--all", "--format", "jsonl")
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatal(code, out, err)
	}
}
func TestDownloadPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.bin")
	os.WriteFile(path, []byte("existing"), 0600)
	code, _, err := invoke(t, "range-error", "", "files", "download", "file", "--output", path)
	if code != 4 {
		t.Fatal(code, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "existing" {
		t.Fatal("failed download damaged existing file")
	}
	code, _, err = invoke(t, "not-modified", "", "files", "download", "file", "--output", path)
	if code != 0 {
		t.Fatal(code, err)
	}
	b, _ = os.ReadFile(path)
	if string(b) != "existing" {
		t.Fatal("304 replaced existing file")
	}
}
func TestProfilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("WAVE_CONFIG_FILE", path)
	var out, err bytes.Buffer
	code := Execute(context.Background(), "test", []string{"config", "set", "--url", "http://localhost:8080", "--key-stdin"}, strings.NewReader("secret-key\n"), &out, &err)
	if code != 0 || strings.Contains(out.String(), "secret-key") {
		t.Fatal(code, out.String(), err.String())
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
}
func TestRejectMixedInput(t *testing.T) {
	code, _, _ := invoke(t, "", "", "tasks", "create", "--session", "s", "--agent", "a", "--input", "x", "--body", "{}")
	if code != 2 {
		t.Fatal(code)
	}
}

func TestUsageExitCodes(t *testing.T) {
	for _, args := range [][]string{{"does-not-exist"}, {"tasks", "wait"}, {"agents", "list", "--unknown"}} {
		code, _, _ := invoke(t, "", "", args...)
		if code != 2 {
			t.Fatalf("%v: %d", args, code)
		}
	}
}
