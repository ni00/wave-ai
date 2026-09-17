package xid

import (
	"math/big"
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

func TestEncodeULIDKeepsEveryRandomBit(t *testing.T) {
	// An all-ones payload must fill all 26 Crockford symbols: 2 pad bits +
	// 128 payload bits = 130 = 26*5. The old tail encoding dropped the final
	// bit and emitted a smaller last symbol.
	var b [16]byte
	for i := range b {
		b[i] = 0xff
	}
	s := encodeULID(b)
	n := new(big.Int)
	for _, c := range s {
		idx := strings.IndexByte(crockford, byte(c))
		if idx < 0 {
			t.Fatalf("non-crockford char %q", c)
		}
		n.Mul(n, big.NewInt(32))
		n.Add(n, big.NewInt(int64(idx)))
	}
	want := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	if n.Cmp(want) != 0 {
		t.Fatalf("encoding dropped bits: %s", s)
	}
	if s[25] != crockford[31] {
		t.Fatalf("last symbol %q loses the low bits", s[25])
	}
}

func TestULIDMonotonicAcrossOverflow(t *testing.T) {
	// Crossing the per-millisecond sequence budget must keep issuing
	// increasing IDs; the old code rewound to the previous bucket.
	prev := ""
	seen := map[string]bool{}
	for i := 0; i < 7000; i++ {
		id := New("t")
		if id <= prev {
			t.Fatalf("not monotonic at %d: %s after %s", i, id, prev)
		}
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
		prev = id
	}
}
