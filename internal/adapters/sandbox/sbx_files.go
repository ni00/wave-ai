package sandbox

import (
	"context"
	"path"

	"fmt"

	"strings"

	"connectrpc.com/connect"

	sbxfilesv1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/files/v1"

	sbx "github.com/docker/sandboxes-api/gen/go/sbx"
)

func (s *Sbx) ReadFile(ctx context.Context, sessionID, path string, maxBytes int64) ([]byte, error) {
	sc, err := s.client(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	dl, err := sc.Files().Download(ctx, connect.NewRequest(&sbxfilesv1.DownloadRequest{Paths: []string{path}}))
	if err != nil {
		return nil, err
	}
	defer dl.Close()
	var buf []byte
	for dl.Receive() {
		msg := dl.Msg()
		switch f := msg.Frame.(type) {
		case *sbxfilesv1.DownloadResponse_Header:
		case *sbxfilesv1.DownloadResponse_Data:

			if maxBytes <= 0 || int64(len(buf)) < maxBytes {
				buf = append(buf, f.Data...)
				if maxBytes > 0 && int64(len(buf)) > maxBytes {
					buf = buf[:maxBytes]
				}
				if maxBytes > 0 && int64(len(buf)) >= maxBytes {
					return buf, nil
				}
			}
		case *sbxfilesv1.DownloadResponse_Error:
			return nil, fmt.Errorf("sbx download %s: %s", f.Error.Path, f.Error.Message)
		}
	}
	if err := dl.Err(); err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(buf)) > maxBytes {
		buf = buf[:maxBytes]
	}
	return buf, nil
}

func (s *Sbx) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	return s.writeFileMode(ctx, sessionID, path, content, 0o644)
}

func (s *Sbx) ListDir(ctx context.Context, sessionID, path string) ([]string, error) {
	sc, err := s.client(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	entries, err := listEntries(ctx, sc, path)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {

		rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, path), "/")
		if e.IsDir {
			rel += "/"
		}
		out = append(out, rel)
	}
	return out, nil
}

// StageInput places input content under the uploads root. The agent user
// is root, so mode bits alone cannot enforce read-only; the verified
// path is a backend RO mount (admission pending — see design doc). The
// 0444 mode is recorded here as the interim marker, not a security claim.
func (s *Sbx) StageInput(ctx context.Context, sessionID, relPath string, content []byte) error {
	return s.writeFileMode(ctx, sessionID, "/mnt/session/uploads/"+relPath, content, 0o444)
}

func (s *Sbx) StageSkill(ctx context.Context, sessionID, name string, files map[string][]byte) error {
	for rel, content := range files {
		if err := s.writeFileMode(ctx, sessionID, "/mnt/skills/"+name+"/"+rel, content, 0o444); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sbx) StageMemory(ctx context.Context, sessionID, mountPath, relPath string, content []byte, writable bool) error {
	mode := uint32(0o444)
	if writable {
		mode = 0o644
	}
	return s.writeFileMode(ctx, sessionID, mountPath+"/"+relPath, content, mode)
}

func (s *Sbx) writeFileMode(ctx context.Context, sessionID, path string, content []byte, mode uint32) error {
	sc, err := s.client(ctx, sessionID)
	if err != nil {
		return err
	}
	up := sc.Files().Upload(ctx)
	if err := up.Send(&sbxfilesv1.UploadRequest{Frame: &sbxfilesv1.UploadRequest_Header{
		Header: &sbxfilesv1.FileHeader{Path: path, Mode: mode},
	}}); err != nil {
		return err
	}
	if err := up.Send(&sbxfilesv1.UploadRequest{Frame: &sbxfilesv1.UploadRequest_Data{Data: content}}); err != nil {
		return err
	}
	_, err = up.CloseAndReceive()
	return err
}

// VisitOutputs bounds memory to one file plus directory metadata.
func (s *Sbx) VisitOutputs(ctx context.Context, sid string, maxFileBytes int64, visit func(string, []byte) error) error {
	sc, err := s.client(ctx, sid)
	if err != nil {
		return err
	}
	if maxFileBytes <= 0 {
		maxFileBytes = 32 << 20
	}
	total, count, entriesSeen := int64(0), 0, 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if depth > 20 {
			return fmt.Errorf("artifact directory depth exceeded")
		}
		entries, err := listEntries(ctx, sc, dir)
		if err != nil {
			return err
		}
		entriesSeen += len(entries)
		if entriesSeen > 10000 {
			return fmt.Errorf("artifact entry limit exceeded")
		}
		for _, entry := range entries {
			if path.Dir(entry.Path) != dir || path.Clean(entry.Path) != entry.Path {
				return fmt.Errorf("invalid artifact directory entry")
			}
			if entry.IsDir {
				if err = walk(entry.Path, depth+1); err != nil {
					return err
				}
				continue
			}
			if entry.Mode&0o170000 != 0o100000 {
				continue
			}
			count++
			if count > 1000 {
				return fmt.Errorf("artifact publication file limit exceeded")
			}
			data, err := s.ReadFile(ctx, sid, entry.Path, maxFileBytes+1)
			if err != nil {
				return err
			}
			if int64(len(data)) > maxFileBytes {
				return fmt.Errorf("artifact exceeds per-file limit")
			}
			total += int64(len(data))
			if total > 128<<20 {
				return fmt.Errorf("artifact publication size limit exceeded")
			}
			if err = visit(entry.Path, data); err != nil {
				return err
			}
		}
		return nil
	}
	return walk("/mnt/session/outputs", 0)
}

func listEntries(ctx context.Context, sc *sbx.SandboxClient, path string) ([]*sbxfilesv1.FileInfo, error) {
	var out []*sbxfilesv1.FileInfo
	token := ""
	for {
		res, e := sc.Files().List(ctx, connect.NewRequest(&sbxfilesv1.ListRequest{Path: path, PageSize: 1000, PageToken: token}))
		if e != nil {
			return nil, e
		}
		out = append(out, res.Msg.Entries...)
		if len(out) > 10000 {
			return nil, fmt.Errorf("directory entry limit exceeded")
		}
		next := res.Msg.NextPageToken
		if next == "" {
			return out, nil
		}
		if next == token {
			return nil, fmt.Errorf("sandbox returned a repeated page token")
		}
		token = next
	}
}
