package console

import (
	"bytes"
	"testing"
)

func TestValidateBench(t *testing.T) {
	good := []byte(`{"version":2,"run_id":"bench_1","kind":"live","started_at":"2026-09-14T00:00:00Z","phases":[{"workers":1,"attempted":1,"succeeded":1,"elapsed_seconds":2}]}`)
	if err := ValidateBench(bytes.Replace(good, []byte(`"workers":1`), []byte(`"workers":0`), 1)); err != nil {
		t.Fatal("live reports use server-managed worker counts", err)
	}
	if err := ValidateBench(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`{}`), bytes.Repeat([]byte("x"), (16<<20)+1), bytes.Replace(good, []byte(`"version":2`), []byte(`"version":1`), 1), bytes.Replace(good, []byte(`"workers":1`), []byte(`"workers":-1`), 1), bytes.Replace(good, []byte(`"succeeded":1`), []byte(`"succeeded":2`), 1), append(append([]byte{}, good...), []byte(` {}`)...)} {
		if ValidateBench(bad) == nil {
			t.Fatal("invalid report accepted")
		}
	}
}
