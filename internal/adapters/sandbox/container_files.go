package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/moby/moby/client"
)

func absolutePath(p string) error {
	if !path.IsAbs(p) || path.Clean(p) != p || strings.ContainsRune(p, 0) {
		return errors.New("sandbox path must be clean and absolute")
	}
	return nil
}
func relativePath(p string) error {
	if p == "" || p == "." || path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsRune(p, 0) {
		return errors.New("invalid relative sandbox path")
	}
	return nil
}
func (c *Container) copyDirectories(ctx context.Context, id string, dirs []string) error {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, dir := range dirs {
		if err := w.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	_, err := c.api.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: "/", Content: &b})
	return err
}
func (c *Container) copyFile(ctx context.Context, id, p string, data []byte, mode int64) error {
	return c.copyFileAt(ctx, id, "/", p, data, mode)
}

func (c *Container) copyFileAt(ctx context.Context, id, destination, p string, data []byte, mode int64) error {
	if err := absolutePath(p); err != nil {
		return err
	}
	if p == "/" {
		return errors.New("file path cannot be root")
	}
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	archiveName := strings.TrimPrefix(strings.TrimPrefix(p, destination), "/")
	if err := relativePath(archiveName); err != nil {
		return err
	}
	parts := strings.Split(path.Dir(archiveName), "/")
	dir := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		dir += part + "/"
		if err := w.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
			return err
		}
	}
	if err := w.WriteHeader(&tar.Header{Name: archiveName, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	_, err := c.api.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: destination, Content: &b})
	return err
}
func (c *Container) WriteFile(ctx context.Context, sid, p string, data []byte) error {
	defer c.lock(sid)()
	r, err := c.ensure(ctx, sid)
	if err != nil {
		return err
	}
	return c.copyFile(ctx, r.BackendID, p, data, 0644)
}
func (c *Container) ReadFile(ctx context.Context, sid, p string, maxBytes int64) ([]byte, error) {
	if err := absolutePath(p); err != nil {
		return nil, err
	}
	defer c.lock(sid)()
	r, err := c.ensure(ctx, sid)
	if err != nil {
		return nil, err
	}
	result, err := c.api.CopyFromContainer(ctx, r.BackendID, client.CopyFromContainerOptions{SourcePath: p})
	if err != nil {
		return nil, err
	}
	defer result.Content.Close()
	tr := tar.NewReader(result.Content)
	h, err := tr.Next()
	if err != nil {
		return nil, err
	}
	if h.Typeflag != tar.TypeReg {
		return nil, errors.New("sandbox path is not a regular file")
	}
	if maxBytes <= 0 {
		maxBytes = 32 << 20
	}
	return io.ReadAll(io.LimitReader(tr, maxBytes))
}
func (c *Container) StageInput(ctx context.Context, sid, rel string, data []byte) error {
	if err := relativePath(rel); err != nil {
		return err
	}
	return c.stage(ctx, sid, "/uploads/"+rel, data)
}
func (c *Container) StageSkill(ctx context.Context, sid, name string, files map[string][]byte) error {
	if err := relativePath(name); err != nil {
		return err
	}
	if path.Base(name) != name {
		return errors.New("skill name must be a single path component")
	}
	for rel, data := range files {
		if err := relativePath(rel); err != nil {
			return err
		}
		if err := c.stage(ctx, sid, "/skills/"+name+"/"+rel, data); err != nil {
			return err
		}
	}
	return nil
}
func (c *Container) stage(ctx context.Context, sid, p string, data []byte) error {
	defer c.lock(sid)()
	r, err := c.ensure(ctx, sid)
	if err != nil {
		return err
	}
	info, err := c.api.ContainerInspect(ctx, r.StagerID, client.ContainerInspectOptions{})
	if err != nil {
		return err
	}
	if err = c.verify(info.Container, r, true); err != nil {
		return err
	}
	if info.Container.State == nil || info.Container.State.Running {
		return errors.New("input staging container must be stopped")
	}
	destination := "/" + strings.Split(strings.TrimPrefix(p, "/"), "/")[0]
	return c.copyFileAt(ctx, r.StagerID, destination, p, data, 0444)
}
func walkArchive(reader io.Reader, root string, maxFileBytes int64, visit func(string, *tar.Header, io.Reader) error) error {
	limited := &io.LimitedReader{R: reader, N: (128 << 20) + (16 << 20)}
	tr := tar.NewReader(limited)
	total := int64(0)
	files := 0
	for entries := 0; ; entries++ {
		h, err := tr.Next()
		if err == io.EOF {
			// N==0 only means the budget was consumed; an archive ending
			// exactly at the budget is valid. Probe the raw stream: any
			// further byte proves truncation.
			if limited.N == 0 {
				var probe [1]byte
				if n, _ := reader.Read(probe[:]); n > 0 {
					return errors.New("sandbox archive size exceeded")
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
		if entries >= 10000 {
			return errors.New("sandbox archive entry limit exceeded")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if err := relativePath(name); err != nil {
			return err
		}
		if name != root && !strings.HasPrefix(name, root+"/") {
			return errors.New("archive entry escapes requested root")
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
		if strings.Count(rel, "/") > 20 {
			return errors.New("sandbox directory depth exceeded")
		}
		if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Typeflag == tar.TypeReg {
			files++
			total += h.Size
			if h.Size < 0 || h.Size > maxFileBytes || total > 128<<20 || files > 1000 {
				return errors.New("sandbox archive file/size limit exceeded")
			}
		}
		if err = visit(rel, h, tr); err != nil {
			return err
		}
	}
}

// parseFindListing decodes the NUL-separated `find -printf '%y\0%P\0'`
// stream: the first pair is the listing root itself (type "d", empty %P) and
// every later pair is one direct child. NUL cannot occur inside a filename,
// so names containing spaces or newlines parse unambiguously. Like the former
// tar-based listing, directories keep a trailing slash and other entry types
// (symlinks, devices, ...) are omitted.
func parseFindListing(out string) ([]string, error) {
	fields := strings.Split(out, "\x00")
	if len(fields) < 2 || fields[len(fields)-1] != "" {
		return nil, errors.New("sandbox listing stream truncated")
	}
	entries := fields[:len(fields)-1]
	if len(entries)%2 != 0 || entries[0] != "d" || entries[1] != "" {
		return nil, errors.New("sandbox path is not a directory")
	}
	names := []string{}
	for i := 2; i < len(entries); i += 2 {
		typ, name := entries[i], entries[i+1]
		if len(typ) != 1 || name == "" {
			return nil, errors.New("sandbox listing stream corrupted")
		}
		switch typ {
		case "d":
			names = append(names, name+"/")
		case "f":
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (c *Container) ListDir(ctx context.Context, sid, p string) ([]string, error) {
	if err := absolutePath(p); err != nil {
		return nil, err
	}
	defer c.lock(sid)()
	r, err := c.ensure(ctx, sid)
	if err != nil {
		return nil, err
	}
	// CopyFromContainer would tar the entire subtree only to discard all but
	// the first level; a one-level find streams exactly the top-level names.
	// The path is a clean absolute path and argv is exec'd directly, so no
	// shell quoting applies. The format string holds escaped (\0), not
	// literal, NULs: argv cannot contain NUL bytes, and find's printf engine
	// emits the real separators into the output stream.
	stdout, stderr, code, err := c.execCapture(ctx, r.BackendID, []string{"find", p, "-maxdepth", "1", "-printf", `%y\0%P\0`})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("sandbox listdir failed (exit %d): %s", code, strings.TrimSpace(stderr))
	}
	return parseFindListing(stdout)
}
func (c *Container) VisitOutputs(ctx context.Context, sid string, maxFileBytes int64, visit func(string, []byte) error) error {
	defer c.lock(sid)()
	r, err := c.ensure(ctx, sid)
	if err != nil {
		return err
	}
	result, err := c.api.CopyFromContainer(ctx, r.BackendID, client.CopyFromContainerOptions{SourcePath: "/mnt/session/outputs"})
	if err != nil {
		return err
	}
	defer result.Content.Close()
	if !result.Stat.Mode.IsDir() {
		return errors.New("outputs must be a directory")
	}
	if maxFileBytes <= 0 {
		maxFileBytes = 32 << 20
	}
	return walkArchive(result.Content, "outputs", maxFileBytes, func(rel string, h *tar.Header, rd io.Reader) error {
		if h.Typeflag != tar.TypeReg {
			return nil
		}
		if rel == "" {
			return fmt.Errorf("invalid output file")
		}
		data, err := io.ReadAll(rd)
		if err != nil {
			return err
		}
		return visit("/mnt/session/outputs/"+rel, data)
	})
}
