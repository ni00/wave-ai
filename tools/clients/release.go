package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (t *tool) packageRelease(ctx context.Context) error {
	if err := t.check(); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "wave-release-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	stage := filepath.Join(work, "artifacts")
	if err = os.Mkdir(stage, 0755); err != nil {
		return err
	}
	version := t.config.Version
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, extension := "wavectl", ".tar.gz"
			if goos == "windows" {
				name += ".exe"
				extension = ".zip"
			}
			binary := filepath.Join(work, name)
			env := map[string]string{"GOOS": goos, "GOARCH": arch, "CGO_ENABLED": "0"}
			fmt.Fprintf(t.out, "Building CLI %s/%s\n", goos, arch)
			if err = t.run(ctx, t.path("cli"), env, "go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version, "-o", binary, "."); err != nil {
				return err
			}
			archive := filepath.Join(stage, fmt.Sprintf("wavectl_%s_%s_%s%s", version, goos, arch, extension))
			if err = writeArchive(archive, []archiveFile{{binary, name}}); err != nil {
				return err
			}
		}
	}
	for _, skill := range []string{"wave-client", "wave-admin"} {
		files, err := archiveFiles(t.path("skills/"+skill), "", nil)
		if err != nil {
			return err
		}
		if err = writeArchive(filepath.Join(stage, skill+"_"+version+".zip"), files); err != nil {
			return err
		}
	}
	python := filepath.Join(work, "python")
	if err = t.run(ctx, t.path("sdks/python"), nil, "uv", "build", "--out-dir", python); err != nil {
		return err
	}
	files, err := treeFiles(python)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err = copyFile(file, filepath.Join(stage, filepath.Base(file))); err != nil {
			return err
		}
	}
	if err = t.run(ctx, t.path("sdks/typescript"), nil, "npm", "run", "build"); err != nil {
		return err
	}
	if err = t.run(ctx, t.path("sdks/typescript"), nil, "npm", "pack", "--ignore-scripts", "--pack-destination", stage); err != nil {
		return err
	}
	sources, err := archiveFiles(t.path("sdks/go"), "wave-go", func(name string) bool {
		return strings.Contains("|.go|.mod|.sum|.json|.md|", "|"+filepath.Ext(name)+"|")
	})
	if err != nil {
		return err
	}
	if err = writeArchive(filepath.Join(stage, "wave-go_"+version+".tar.gz"), sources); err != nil {
		return err
	}
	for _, name := range []string{"openapi.json", "openapi.zh-CN.json"} {
		if err = copyFile(t.path("api/"+name), filepath.Join(stage, name)); err != nil {
			return err
		}
	}
	var manifest object
	if err = readJSON(t.path("tools/clients/manifest.json"), &manifest); err != nil {
		return err
	}
	manifest["packages"] = object{"goModule": t.config.GoModule, "pythonPackage": t.config.PythonPackage, "npmPackage": t.config.NPMPackage}
	manifest["repository"] = "https://github.com/ni00/wave-ai"
	manifest["serverContractVersion"] = "1.0"
	if err = writeJSON(filepath.Join(stage, "manifest.json"), manifest); err != nil {
		return err
	}
	if err = writeChecksums(stage); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	destination := t.path("dist/clients/" + version)
	if err = replacePath(stage, destination); err != nil {
		return err
	}
	fmt.Fprintln(t.out, "Local artifacts:", destination)
	return nil
}
