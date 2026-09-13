package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
)

type checkpoint struct {
	Session  string `json:"session_id"`
	Sequence string `json:"sequence"`
}

func readCheckpoint(path, session string) (string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	var point checkpoint
	if err = json.NewDecoder(file).Decode(&point); err != nil {
		return "", fmt.Errorf("invalid cursor file: %w", err)
	}
	if point.Session != session {
		return "", usage("cursor file belongs to session %s, not %s", point.Session, session)
	}
	n, err := strconv.ParseInt(point.Sequence, 10, 64)
	if err != nil || n < 0 {
		return "", usage("invalid cursor sequence")
	}
	return point.Sequence, nil
}
func writeCheckpoint(path, session, sequence string) error {
	n, err := strconv.ParseInt(sequence, 10, 64)
	if err != nil || n < 0 {
		return fmt.Errorf("server returned an invalid event ID")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(checkpoint{Session: session, Sequence: sequence})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func (i *operationInput) watch(cmd *cobra.Command, ctx context.Context, c *wave.Client, options wave.Options) error {
	session := options.Path["id"]
	last := options.Headers.Get("Last-Event-ID")
	if i.cursorFile != "" {
		var err error
		last, err = readCheckpoint(i.cursorFile, session)
		if err != nil {
			return err
		}
	}
	return c.Events(ctx, session, wave.StreamOptions{After: options.Query.Get("after"), LastEventID: last, TaskID: i.taskFilter, MaxReconnects: i.reconnects}, func(event wave.Event) error {
		if err := emit(cmd, event); err != nil {
			return err
		}
		if i.cursorFile != "" {
			return writeCheckpoint(i.cursorFile, session, event.ID)
		}
		return nil
	})
}
