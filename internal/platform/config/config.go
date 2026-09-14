package config

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"wave-ai.local/wave/internal/platform/blobstore"
)

type Config struct {
	Role                  string
	HTTPAddr              string
	DatabaseURL           string
	DataDir               string
	StorageBackend        string
	S3                    blobstore.S3Options
	ModelBaseURL          string
	ModelAPIKey           string
	ModelTimeoutSec       int
	ContextTokens         int
	WorkerConcurrency     int
	LeaseSeconds          int
	SandboxBackend        string
	SandboxLocalRoot      string
	DockerHost            string
	PodmanHost            string
	ContainerImage        string
	ContainerNetwork      string
	GVisorRuntime         string
	SbxURL                string
	SbxToken              string
	SbxImage              string
	SbxCPUs               int
	SbxMemoryMiB          int
	SbxMaxRunning         int
	SbxMemoryBudgetMiB    int
	MasterKey             []byte
	GuardrailInputBlock   []string
	GuardrailOutputRedact []string
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
	resourceInteger := func(primary, legacy string, fallback int) int {
		if os.Getenv(primary) != "" {
			return integer(primary, fallback)
		}
		return integer(legacy, fallback)
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
		ModelBaseURL:       str("WAVE_MODEL_BASE_URL", "https://api.openai.com/v1"),
		ModelAPIKey:        os.Getenv("WAVE_MODEL_API_KEY"),
		SandboxBackend:     str("WAVE_SANDBOX_BACKEND", "gvisor"),
		DockerHost:         str("WAVE_DOCKER_HOST", "unix:///var/run/docker.sock"),
		PodmanHost:         str("WAVE_PODMAN_HOST", "unix:///run/wave-podman/podman.sock"),
		ContainerImage:     str("WAVE_CONTAINER_IMAGE", "docker.io/library/python:3.12-slim-bookworm"),
		ContainerNetwork:   str("WAVE_CONTAINER_NETWORK", "none"),
		GVisorRuntime:      str("WAVE_GVISOR_RUNTIME", "wave-runsc"),
		SandboxLocalRoot:   str("WAVE_SANDBOX_LOCAL_ROOT", dir+"/sandboxes"),
		SbxURL:             str("WAVE_SBX_URL", "http://localhost:6060"),
		SbxToken:           os.Getenv("WAVE_SBX_TOKEN"),
		SbxImage:           str("WAVE_SBX_IMAGE", "docker.io/docker/sandbox-ubuntu-24.04:latest"),
		WorkerConcurrency:  integer("WAVE_WORKER_CONCURRENCY", 10),
		LeaseSeconds:       integer("WAVE_LEASE_SECONDS", 30),
		ModelTimeoutSec:    integer("WAVE_MODEL_TIMEOUT_SEC", 300),
		SbxCPUs:            resourceInteger("WAVE_SANDBOX_CPUS", "WAVE_SBX_CPUS", 1),
		SbxMemoryMiB:       resourceInteger("WAVE_SANDBOX_MEMORY_MIB", "WAVE_SBX_MEMORY_MIB", 1024),
		SbxMaxRunning:      resourceInteger("WAVE_SANDBOX_MAX_RUNNING", "WAVE_SBX_MAX_RUNNING", 2),
		SbxMemoryBudgetMiB: resourceInteger("WAVE_SANDBOX_MEMORY_BUDGET_MIB", "WAVE_SBX_MEMORY_BUDGET_MIB", 4096),
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
	if c.SbxCPUs > 16 || c.SbxMemoryMiB < 512 || c.SbxMemoryMiB > 32768 {
		return nil, fmt.Errorf("sandbox resources require 1–16 CPUs and 512–32768 MiB; verify support with your sandbox runtime")
	}
	if c.SbxMemoryBudgetMiB < c.SbxMemoryMiB {
		return nil, fmt.Errorf("WAVE_SBX_MEMORY_BUDGET_MIB must fit WAVE_SBX_MEMORY_MIB")
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
	switch c.SandboxBackend {
	case "sbx", "gvisor", "podman":
	case "local":
		if os.Getenv("WAVE_SANDBOX_ALLOW_LOCAL") != "true" {
			return nil, fmt.Errorf("local backend requires WAVE_SANDBOX_ALLOW_LOCAL=true")
		}
	default:
		return nil, fmt.Errorf("invalid sandbox backend")
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
