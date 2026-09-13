package xid

import (
	"strings"
	"testing"
	"time"
)

func TestNewPrefixes(t *testing.T) {
	for _, prefix := range []string{"agent", "sess", "sevt", "file", "depl", "drun", "vlt", "vcrd", "skill", "memstore", "mem", "memver", "sesrsc"} {
		id := New(prefix)
		if !strings.HasPrefix(id, prefix+"_") {
			t.Errorf("id %q missing prefix %q", id, prefix)
		}
	}
	// uniqueness under burst
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := New("sess")
		if seen[id] {
			t.Fatalf("collision on %q", id)
		}
		seen[id] = true
	}
}

func TestNewEnvUUIDForm(t *testing.T) {
	id := NewEnv()
	if !strings.HasPrefix(id, "env_") {
		t.Fatalf("id = %q", id)
	}
	uuid := strings.TrimPrefix(id, "env_")
	parts := strings.Split(uuid, "-")
	if len(parts) != 5 || len(parts[0]) != 8 || len(parts[4]) != 12 {
		t.Fatalf("not a canonical uuid: %q", uuid)
	}
	// version nibble 7
	if parts[2][0] != '7' {
		t.Errorf("expected uuid v7 version nibble, got %q", parts[2])
	}
}

func TestSkillVersionForm(t *testing.T) {
	v := SkillVersion(time.UnixMicro(1000))
	if v != "1000" {
		t.Errorf("epoch-micros form = %q", v)
	}
}
