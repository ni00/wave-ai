package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type archiveFile struct{ source, name string }

func archiveFiles(root, prefix string, include func(string) bool) ([]archiveFile, error) {
	names, err := treeFiles(root)
	if err != nil {
		return nil, err
	}
	var files []archiveFile
	for _, name := range names {
		rel, _ := filepath.Rel(root, name)
		if include == nil || include(rel) {
			files = append(files, archiveFile{name, filepath.ToSlash(filepath.Join(prefix, rel))})
		}
	}
	return files, nil
}
func safeArchiveName(name string) bool {
	return !strings.Contains(name, "\\") && filepath.IsLocal(name) && filepath.ToSlash(filepath.Clean(name)) == name
}

func writeArchive(path string, files []archiveFile) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	var add func(archiveFile) error
	if strings.HasSuffix(path, ".zip") {
		writer := zip.NewWriter(f)
		defer func() {
			if closeErr := writer.Close(); err == nil {
				err = closeErr
			}
		}()
		add = func(file archiveFile) error {
			info, err := os.Stat(file.source)
			if err != nil {
				return err
			}
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = file.name
			header.Method = zip.Deflate
			dst, err := writer.CreateHeader(header)
			if err != nil {
				return err
			}
			return streamFile(file.source, dst)
		}
	} else {
		gz := gzip.NewWriter(f)
		defer func() {
			if closeErr := gz.Close(); err == nil {
				err = closeErr
			}
		}()
		writer := tar.NewWriter(gz)
		defer func() {
			if closeErr := writer.Close(); err == nil {
				err = closeErr
			}
		}()
		add = func(file archiveFile) error {
			info, err := os.Stat(file.source)
			if err != nil {
				return err
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = file.name
			if err = writer.WriteHeader(header); err != nil {
				return err
			}
			return streamFile(file.source, writer)
		}
	}
	for _, file := range files {
		if !safeArchiveName(file.name) {
			return fmt.Errorf("invalid archive member %q", file.name)
		}
		if err = add(file); err != nil {
			return err
		}
	}
	return nil
}
func streamFile(path string, dst io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(dst, f)
	return err
}

// Read only the requested regular file; never extract archive-supplied paths.
func archiveMember(path, name string) ([]byte, error) {
	if !safeArchiveName(name) {
		return nil, fmt.Errorf("invalid archive member %q", name)
	}
	if strings.HasSuffix(path, ".zip") {
		reader, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.Name == name {
				if !file.Mode().IsRegular() {
					return nil, fmt.Errorf("archive member is not a regular file: %s", name)
				}
				r, err := file.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				return io.ReadAll(r)
			}
		}
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if header.Name == name {
				if header.Typeflag != tar.TypeReg {
					return nil, fmt.Errorf("archive member is not a regular file: %s", name)
				}
				return io.ReadAll(reader)
			}
		}
	}
	return nil, fmt.Errorf("missing archive member %s in %s", name, path)
}

func writeChecksums(dir string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, file := range files {
		if file.Name() == "SHA256SUMS" {
			continue
		}
		if !file.Type().IsRegular() {
			return fmt.Errorf("unexpected release entry %s", file.Name())
		}
		sum, err := hashFile(filepath.Join(dir, file.Name()))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, file.Name())
	}
	return writeFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0644)
}
func verifyChecksums(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || !safeArchiveName(name) || filepath.Base(name) != name || seen[name] || name == "SHA256SUMS" {
			return fmt.Errorf("invalid release checksum entry %q", line)
		}
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release artifact is not a regular file: %s", name)
		}
		actual, err := hashFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if actual != sum {
			return fmt.Errorf("checksum mismatch: %s", name)
		}
		seen[name] = true
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.Name() != "SHA256SUMS" && !seen[file.Name()] {
			return fmt.Errorf("release artifact missing from SHA256SUMS: %s", file.Name())
		}
	}
	return nil
}
