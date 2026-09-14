package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHelpWithoutConfiguration(t *testing.T) {
	t.Setenv("WAVE_LEASE_SECONDS", "invalid")
	for _, args := range [][]string{{"--help"}, {"serve", "--help"}, {"bootstrap", "--help"}, {"init-db", "--help"}, {"config", "--help"}, {"bench", "--help"}, {"trace", "--help"}, {"bench", "live", "--help"}, {"bench", "compare", "--help"}} {
		var out, diagnostics bytes.Buffer
		if err := runCommand(context.Background(), args, &out, &diagnostics); err != nil {
			t.Fatal(args, err)
		}
		if out.Len()+diagnostics.Len() == 0 {
			t.Fatal("missing help", args)
		}
	}
}
func TestConfigCheckRedactsSecrets(t *testing.T) {
	t.Setenv("WAVE_STORAGE_BACKEND", "local")
	t.Setenv("WAVE_SANDBOX_BACKEND", "sbx")
	t.Setenv("WAVE_MODEL_API_KEY", "private-model-key")
	t.Setenv("WAVE_DATABASE_URL", "postgres://user:private-password@localhost/db")
	var out, diagnostics bytes.Buffer
	if err := runCommand(context.Background(), []string{"config", "check"}, &out, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "private-") || !json.Valid(out.Bytes()) {
		t.Fatal(out.String())
	}
}
func TestInvalidAdminArgumentsBeforeDatabase(t *testing.T) {
	t.Setenv("WAVE_DATABASE_URL", "not-a-database")
	for _, args := range [][]string{{"init-db", "extra"}, {"bootstrap"}, {"bootstrap", "-org", "test", "-format", "bad"}} {
		var out, diagnostics bytes.Buffer
		err := runCommand(context.Background(), args, &out, &diagnostics)
		if err == nil || strings.Contains(err.Error(), "connect") {
			t.Fatal(args, err)
		}
	}
}
