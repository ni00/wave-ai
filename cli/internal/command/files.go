package command

import (
	"context"
	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func download(cmd *cobra.Command, ctx context.Context, c *wave.Client, op, id string, headers http.Header, path string) error {
	if path == "-" {
		_, e := c.Download(ctx, op, id, headers, cmd.OutOrStdout())
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".wave-download-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	result, e := c.Download(ctx, op, id, headers, f)
	if e != nil {
		return e
	}
	if result.StatusCode != 304 {
		if e = f.Sync(); e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		if e = os.Rename(f.Name(), path); e != nil {
			return e
		}
	}
	return emit(cmd, map[string]any{"path": path, "status": result.StatusCode, "bytes": result.Bytes, "headers": result.Header})
}

func upload(cmd *cobra.Command, ctx context.Context, c *wave.Client, operation, path, filename string) error {
	var source io.Reader = cmd.InOrStdin()
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		source = file
		if filename == "" {
			filename = filepath.Base(path)
		}
	}
	result, err := c.Upload(ctx, operation, filename, source, nil)
	if err != nil {
		return err
	}
	return emit(cmd, result.Data)
}
