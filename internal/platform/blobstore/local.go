package blobstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
)

type local struct{ root string }

func New(dataDir string) (*Store, error) {
	root := filepath.Join(dataDir, "blobs")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	return &Store{backend: &local{root: root}}, nil
}

func (l *local) put(ctx context.Context, key string, data []byte) error {
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return err
	}
	defer root.Close()
	dir := path.Dir(key)
	if err = root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Resolve all subsequent operations inside this owner's directory, including
	// symlinks. An object cannot redirect a write into another owner's namespace.
	owner, err := root.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer owner.Close()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	temp := ".upload-" + hex.EncodeToString(random[:])
	f, err := owner.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { f.Close(); owner.Remove(temp) }()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return owner.Rename(temp, path.Base(key))
}

func (l *local) open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	owner, err := root.OpenRoot(path.Dir(key))
	if err != nil {
		return nil, err
	}
	defer owner.Close()
	return owner.Open(path.Base(key))
}
