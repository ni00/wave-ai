package bench

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"strconv"
	"strings"
)

// Keep JSON numbers exact: float64 would make different integers above 2^53
// compare equal and could incorrectly validate an agent's artifact.
func equalJSON(a, b []byte) bool {
	decode := func(raw []byte) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var v, extra any
		if d.Decode(&v) != nil || d.Decode(&extra) != io.EOF {
			return nil, false
		}
		return v, true
	}
	left, ok := decode(a)
	if !ok {
		return false
	}
	right, ok := decode(b)
	return ok && equalJSONValue(left, right)
}

func equalJSONValue(a, b any) bool {
	switch a := a.(type) {
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for k, v := range a {
			other, present := b[k]
			if !present || !equalJSONValue(v, other) {
				return false
			}
		}
		return true
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i, v := range a {
			if !equalJSONValue(v, b[i]) {
				return false
			}
		}
		return true
	case json.Number:
		b, ok := b.(json.Number)
		if !ok {
			return false
		}
		if a == b {
			return true
		}
		left, lok := boundedNumber(a)
		right, rok := boundedNumber(b)
		return lok && rok && left.Cmp(right) == 0
	default:
		return a == b // JSON scalars: string, bool, null.
	}
}

func boundedNumber(n json.Number) (*big.Rat, bool) {
	s := n.String()
	if len(s) > 4096 {
		return nil, false
	}
	if _, exp, ok := strings.Cut(strings.ToLower(s), "e"); ok {
		value, err := strconv.Atoi(exp)
		if err != nil || value < -4096 || value > 4096 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
}
