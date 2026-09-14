package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestSandboxBenchmarkValidation(t *testing.T) {
	for _, args := range [][]string{{"-concurrency", "0"}, {"-concurrency", "2,2"}, {"-memory-mib", "128"}, {"-memory-mib", "1024,1024"}, {"-cpus", "0"}, {"-iterations", "0"}, {"-timeout", "0s"}, {"-format", "csv"}, {"-command", " "}, {"unexpected"}} {
		err := runSandbox(context.Background(), args, io.Discard, io.Discard)
		if err == nil || strings.Contains(err.Error(), "sbx API unavailable") {
			t.Fatalf("flags were not rejected before network access: %v: %v", args, err)
		}
	}
}

func TestSandboxReportRetainsCleanupFailure(t *testing.T) {
	var out bytes.Buffer
	err := writeSandboxReport(&out, sandboxOptions{Format: "json", CPUs: 1}, []sandboxSample{{Name: "wave-bench-example", MemoryMiB: 1024, Concurrency: 1, Error: "command exited 137", CleanupError: "backend unavailable"}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Samples []sandboxSample `json:"samples"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Samples) != 1 || decoded.Samples[0].CleanupError == "" || decoded.Samples[0].Name != "wave-bench-example" {
		t.Fatal("cleanup failure lost", out.String())
	}
}
