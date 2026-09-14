package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirectoryLocation(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	for _, tc := range []struct{ name, explicit, xdg, want string }{
		{"explicit", filepath.Join(base, "custom"), base, filepath.Join(base, "custom")},
		{"xdg", "", base, filepath.Join(base, "wave-ai")},
		{"default", "", "", filepath.Join(home, ".local", "share", "wave-ai")},
		{"relative xdg ignored", "", "relative", filepath.Join(home, ".local", "share", "wave-ai")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WAVE_DATA_DIR", tc.explicit)
			t.Setenv("XDG_DATA_HOME", tc.xdg)
			got, err := dataDir()
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestStorageConfiguration(t *testing.T) {
	t.Setenv("WAVE_SANDBOX_BACKEND", "sbx")
	t.Setenv("WAVE_ROLE", "all")
	t.Setenv("WAVE_S3_ENDPOINT", "")
	t.Setenv("WAVE_S3_PREFIX", "wave")
	t.Setenv("WAVE_S3_PATH_STYLE", "false")
	t.Setenv("WAVE_S3_ALLOW_HTTP", "false")
	t.Setenv("WAVE_MASTER_KEY", "")
	t.Setenv("WAVE_STORAGE_BACKEND", "")
	t.Setenv("WAVE_S3_BUCKET", "")
	defaults, err := Load()
	if err != nil || defaults.StorageBackend != "s3" || defaults.S3.Bucket != "wave" {
		t.Fatalf("default storage: %v", err)
	}
	t.Setenv("WAVE_S3_BUCKET", "invalid bucket")
	if _, err := Load(); err == nil {
		t.Fatal("invalid bucket accepted")
	}
	t.Setenv("WAVE_S3_BUCKET", "private-bucket")
	t.Setenv("WAVE_S3_REGION", "us-east-1")
	cfg, err := Load()
	if err != nil || cfg.StorageBackend != "s3" || cfg.S3.Region != "us-east-1" {
		t.Fatalf("S3 config: %v", err)
	}
	t.Setenv("WAVE_S3_ENDPOINT", "http://localhost:9000")
	if _, err := Load(); err == nil {
		t.Fatal("plaintext S3 accepted without opt-in")
	}
	t.Setenv("WAVE_S3_ALLOW_HTTP", "true")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WAVE_S3_PATH_STYLE", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	t.Setenv("WAVE_S3_PATH_STYLE", "false")
	t.Setenv("WAVE_STORAGE_BACKEND", "unknown")
	if _, err := Load(); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

func TestSandboxBudgets(t *testing.T) {
	t.Setenv("WAVE_STORAGE_BACKEND", "local")
	t.Setenv("WAVE_SANDBOX_BACKEND", "sbx")
	t.Setenv("WAVE_MASTER_KEY", "")
	for _, name := range []string{"WAVE_SBX_CPUS", "WAVE_SBX_MEMORY_MIB", "WAVE_SBX_MAX_RUNNING", "WAVE_SBX_MEMORY_BUDGET_MIB"} {
		t.Setenv(name, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SbxCPUs != 1 || cfg.SbxMemoryMiB != 1024 || cfg.SbxMaxRunning != 2 || cfg.SbxMemoryBudgetMiB != 4096 {
		t.Fatal("unexpected sandbox defaults")
	}
	for _, tc := range []struct{ key, value string }{{"WAVE_SBX_CPUS", "4294967297"}, {"WAVE_SBX_MEMORY_MIB", "128"}, {"WAVE_SBX_MAX_RUNNING", "0"}, {"WAVE_SBX_MEMORY_BUDGET_MIB", "512"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid sandbox budget accepted")
			}
		})
	}
}

func TestSandboxBackendSelection(t *testing.T) {
	t.Setenv("WAVE_STORAGE_BACKEND", "local")
	t.Setenv("WAVE_MASTER_KEY", "")
	t.Setenv("WAVE_SANDBOX_BACKEND", "")
	t.Setenv("WAVE_SBX_MEMORY_MIB", "superseded-invalid-value")
	t.Setenv("WAVE_SANDBOX_MEMORY_MIB", "1024")
	c, err := Load()
	if err != nil || c.SandboxBackend != "gvisor" || c.SbxMemoryMiB != 1024 {
		t.Fatalf("defaults/precedence: %+v %v", c, err)
	}
	for _, backend := range []string{"gvisor", "podman", "sbx"} {
		t.Setenv("WAVE_SANDBOX_BACKEND", backend)
		c, err = Load()
		if err != nil || c.SandboxBackend != backend {
			t.Fatalf("%s: %v", backend, err)
		}
	}
	t.Setenv("WAVE_SANDBOX_BACKEND", "runc")
	if _, err = Load(); err == nil {
		t.Fatal("unisolated backend accepted")
	}
}
