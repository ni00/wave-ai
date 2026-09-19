package config

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"wave-ai.local/wave/internal/platform/blobstore"
)

type Config struct {
	Role                   string
	HTTPAddr               string
	DatabaseURL            string
	DataDir                string
	StorageBackend         string
	S3                     blobstore.S3Options
	ModelBaseURL           string
	ModelAPIKey            string
	ModelTimeoutSec        int
	ContextTokens          int
	WorkerConcurrency      int
	LeaseSeconds           int
	DeltaFlushBytes        int
	DeltaFlushMS           int
	AuthCacheTTLSeconds    int
	SandboxBackend         string
	DockerHost             string
	ContainerImage         string
	ContainerNetwork       string
	GVisorRuntime          string
	SandboxCPUs            int
	SandboxMemoryMiB       int
	SandboxMaxRunning      int
	SandboxMemoryBudgetMiB int
	SandboxWorkspaceGiB    int
	MasterKey              []byte
	GuardrailInputBlock    []string
	GuardrailOutputRedact  []string
}

func Load() (*Config, error) {
	str := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	var parseErr error
	boolean := func(k string) bool {
		raw := os.Getenv(k)
		if raw == "" {
			return false
		}
		value, err := strconv.ParseBool(raw)
		if err != nil {
			parseErr = fmt.Errorf("%s must be a boolean", k)
		}
		return value
	}
	integer := func(k string, d int) int {
		v := os.Getenv(k)
		if v == "" {
			return d
		}
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 {
			parseErr = fmt.Errorf("%s must be a positive integer", k)
		}
		return n
	}
	// nonNegative accepts 0, which switches a feature off (workspace quota).
	nonNegative := func(k string, d int) int {
		v := os.Getenv(k)
		if v == "" {
			return d
		}
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 {
			parseErr = fmt.Errorf("%s must be a non-negative integer", k)
		}
		return n
	}
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	c := &Config{
		Role:           str("WAVE_ROLE", "all"),
		HTTPAddr:       str("WAVE_HTTP_ADDR", ":8080"),
		DatabaseURL:    DatabaseURL(),
		DataDir:        dir,
		StorageBackend: str("WAVE_STORAGE_BACKEND", "s3"),
		S3: blobstore.S3Options{
			Bucket: str("WAVE_S3_BUCKET", "wave"), Region: os.Getenv("WAVE_S3_REGION"),
			Endpoint: os.Getenv("WAVE_S3_ENDPOINT"), Prefix: str("WAVE_S3_PREFIX", "wave"),
			PathStyle: boolean("WAVE_S3_PATH_STYLE"), AllowHTTP: boolean("WAVE_S3_ALLOW_HTTP"),
		},
		ModelBaseURL:           str("WAVE_MODEL_BASE_URL", "https://api.openai.com/v1"),
		ModelAPIKey:            os.Getenv("WAVE_MODEL_API_KEY"),
		SandboxBackend:         str("WAVE_SANDBOX_BACKEND", "gvisor"),
		DockerHost:             str("WAVE_DOCKER_HOST", "unix:///var/run/docker.sock"),
		ContainerImage:         str("WAVE_CONTAINER_IMAGE", "docker.io/library/python:3.12-slim-bookworm"),
		ContainerNetwork:       str("WAVE_CONTAINER_NETWORK", "none"),
		GVisorRuntime:          str("WAVE_GVISOR_RUNTIME", "wave-runsc"),
		WorkerConcurrency:      integer("WAVE_WORKER_CONCURRENCY", 10),
		LeaseSeconds:           integer("WAVE_LEASE_SECONDS", 30),
		DeltaFlushBytes:        integer("WAVE_DELTA_FLUSH_BYTES", 4096),
		DeltaFlushMS:           integer("WAVE_DELTA_FLUSH_MS", 500),
		AuthCacheTTLSeconds:    integer("WAVE_AUTH_CACHE_TTL_SEC", 15),
		ModelTimeoutSec:        integer("WAVE_MODEL_TIMEOUT_SEC", 300),
		SandboxCPUs:            integer("WAVE_SANDBOX_CPUS", 1),
		SandboxMemoryMiB:       integer("WAVE_SANDBOX_MEMORY_MIB", 1024),
		SandboxMaxRunning:      integer("WAVE_SANDBOX_MAX_RUNNING", 2),
		SandboxMemoryBudgetMiB: integer("WAVE_SANDBOX_MEMORY_BUDGET_MIB", 4096),
		SandboxWorkspaceGiB:    nonNegative("WAVE_SANDBOX_WORKSPACE_GIB", 0),
	}
	c.ContextTokens = integer("WAVE_CONTEXT_TOKENS", 32000)
	if raw := os.Getenv("WAVE_GUARDRAIL_INPUT_BLOCK"); raw != "" {
		c.GuardrailInputBlock = strings.Split(raw, "\n")
	}
	if raw := os.Getenv("WAVE_GUARDRAIL_OUTPUT_REDACT"); raw != "" {
		c.GuardrailOutputRedact = strings.Split(raw, "\n")
	}
	if parseErr != nil {
		return nil, parseErr
	}
	if c.SandboxCPUs > 16 || c.SandboxMemoryMiB < 512 || c.SandboxMemoryMiB > 32768 {
		return nil, fmt.Errorf("sandbox resources require 1–16 CPUs and 512–32768 MiB; verify support with your sandbox runtime")
	}
	if c.SandboxWorkspaceGiB < 0 || c.SandboxWorkspaceGiB > 1024 {
		return nil, fmt.Errorf("WAVE_SANDBOX_WORKSPACE_GIB must be between 0 and 1024")
	}
	if c.SandboxMemoryBudgetMiB < c.SandboxMemoryMiB {
		return nil, fmt.Errorf("WAVE_SANDBOX_MEMORY_BUDGET_MIB must fit WAVE_SANDBOX_MEMORY_MIB")
	}
	switch c.StorageBackend {
	case "local":
	case "s3":
		if err := c.S3.Validate(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid WAVE_STORAGE_BACKEND")
	}
	switch c.Role {
	case "all", "api", "worker", "scheduler":
	default:
		return nil, fmt.Errorf("invalid WAVE_ROLE")
	}
	if c.SandboxBackend != "gvisor" {
		return nil, fmt.Errorf("WAVE_SANDBOX_BACKEND must be gvisor")
	}
	if raw := os.Getenv("WAVE_MASTER_KEY"); raw != "" {
		b, e := hex.DecodeString(raw)
		if e != nil || len(b) != 32 {
			return nil, fmt.Errorf("WAVE_MASTER_KEY requires 32-byte hex")
		}
		c.MasterKey = b
	}
	if c.LeaseSeconds < 5 {
		return nil, fmt.Errorf("WAVE_LEASE_SECONDS must be at least 5")
	}
	return c, nil
}

// DatabaseURL is also used by database-only administration commands, which do
// not need model, sandbox, storage or master-key configuration.
func DatabaseURL() string {
	if value := os.Getenv("WAVE_DATABASE_URL"); value != "" {
		return value
	}
	slog.Warn("database credentials falling back to the built-in weak default; set WAVE_DATABASE_URL or WAVE_DB_PASSWORD")
	return "postgres://wave:wave@localhost:5432/wave?sslmode=disable"
}

// dataDir keeps persistent local state independent of the source checkout.
func dataDir() (string, error) {
	if dir := os.Getenv("WAVE_DATA_DIR"); dir != "" {
		return dir, nil
	}
	if base := os.Getenv("XDG_DATA_HOME"); base != "" && filepath.IsAbs(base) {
		return filepath.Join(base, "wave-ai"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "wave-ai"), nil
}
