// Package blobstore stores immutable file, skill and artifact content.
// Business authorization happens before storage access; every key is additionally
// bound to an organization and owner in both backends.
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type Scope struct{ OrgID, OwnerID string }

type backend interface {
	put(context.Context, string, []byte) error
	open(context.Context, string) (io.ReadSeekCloser, error)
}

type Store struct{ backend backend }

var component = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (s Scope) prefix() (string, error) {
	if !component.MatchString(s.OrgID) || !component.MatchString(s.OwnerID) {
		return "", errors.New("invalid blob owner scope")
	}
	return s.OrgID + "/" + s.OwnerID + "/", nil
}

func (s *Store) PutBytes(ctx context.Context, scope Scope, data []byte) (string, int64, error) {
	prefix, err := scope.prefix()
	if err != nil {
		return "", 0, err
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	key := prefix + hex.EncodeToString(sum[:])
	if err := s.backend.put(ctx, key, data); err != nil {
		return "", 0, err
	}
	return key, int64(len(data)), nil
}

func (s *Store) Open(ctx context.Context, scope Scope, key string) (io.ReadSeekCloser, error) {
	prefix, err := scope.prefix()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(key, prefix) || !digest.MatchString(strings.TrimPrefix(key, prefix)) {
		return nil, errors.New("blob key outside owner scope")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.backend.open(ctx, key)
}

// ReadAll is for bounded staging into a sandbox, not HTTP downloads.
func (s *Store) ReadAll(ctx context.Context, scope Scope, key string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("blob read limit must be positive")
	}
	f, err := s.Open(ctx, scope, key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, errors.New("blob exceeds read limit")
	}
	sum := sha256.Sum256(b)
	if !strings.HasSuffix(key, fmt.Sprintf("/%x", sum)) {
		return nil, errors.New("blob checksum mismatch")
	}
	return b, nil
}
