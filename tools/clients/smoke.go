package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func oneMatch(pattern string) (string, error) {
	names, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(names) != 1 {
		return "", fmt.Errorf("expected one artifact matching %s, found %d", pattern, len(names))
	}
	return names[0], nil
}

func (t *tool) smoke(ctx context.Context) error {
	artifacts := t.path("dist/clients/" + t.config.Version)
	if err := verifyChecksums(artifacts); err != nil {
		return err
	}
	for _, name := range []string{"openapi.json", "openapi.zh-CN.json"} {
		same, err := samePath(filepath.Join(artifacts, name), t.path("api/"+name))
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("stale contract archive: %s", name)
		}
	}
	var operations object
	if err := readJSON(t.path("tools/clients/operations.json"), &operations); err != nil {
		return err
	}
	count := len(operations)
	work, err := os.MkdirTemp("", "wave-install-smoke-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err = t.run(ctx, work, nil, "uv", "venv", filepath.Join(work, "venv")); err != nil {
		return err
	}
	python := filepath.Join(work, "venv/bin/python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(work, "venv/Scripts/python.exe")
	}
	wheel, err := oneMatch(filepath.Join(artifacts, "*.whl"))
	if err != nil {
		return err
	}
	if err = t.run(ctx, work, nil, "uv", "pip", "install", "--python", python, wheel); err != nil {
		return err
	}
	pythonCheck := fmt.Sprintf(`from wave_ai import Client, AsyncClient, OPERATIONS; from wave_ai_generated import AgentsApi; assert len(OPERATIONS)==%d; assert Client().raw().configuration.host == "http://localhost:8080"`, count)
	if err = t.run(ctx, work, nil, python, "-c", pythonCheck); err != nil {
		return err
	}
	if err = writeJSON(filepath.Join(work, "package.json"), object{"private": true, "type": "module"}); err != nil {
		return err
	}
	npm, err := oneMatch(filepath.Join(artifacts, "*.tgz"))
	if err != nil {
		return err
	}
	if err = t.run(ctx, work, nil, "npm", "install", "--ignore-scripts", npm); err != nil {
		return err
	}
	packageName, _ := json.Marshal(t.config.NPMPackage)
	js := fmt.Sprintf(`import {Client,operations,generated} from %s; if(Object.keys(operations).length!==%d || !new generated.AgentsApi(new Client().rawConfiguration())) process.exit(1);`, packageName, count)
	if err = t.run(ctx, work, nil, "node", "--input-type=module", "-e", js); err != nil {
		return err
	}
	name, extension := "wavectl", ".tar.gz"
	if runtime.GOOS == "windows" {
		name += ".exe"
		extension = ".zip"
	}
	archive := filepath.Join(artifacts, fmt.Sprintf("wavectl_%s_%s_%s%s", t.config.Version, runtime.GOOS, runtime.GOARCH, extension))
	data, err := archiveMember(archive, name)
	if err != nil {
		return err
	}
	binary := filepath.Join(work, name)
	if err = writeFile(binary, data, 0755); err != nil {
		return err
	}
	output, err := t.output(ctx, work, map[string]string{"WAVE_CONFIG_FILE": filepath.Join(work, "cli-config.json"), "WAVE_PROFILE": ""}, binary, "schema")
	if err != nil {
		return err
	}
	var schema object
	if err = json.Unmarshal([]byte(output), &schema); err != nil {
		return err
	}
	if len(obj(schema["operations"])) != count {
		return fmt.Errorf("CLI archive operation count differs from contract")
	}
	for _, skill := range []string{"wave-client", "wave-admin"} {
		archive := filepath.Join(artifacts, skill+"_"+t.config.Version+".zip")
		files, err := archiveFiles(t.path("skills/"+skill), "", nil)
		if err != nil {
			return err
		}
		for _, file := range files {
			want, err := os.ReadFile(file.source)
			if err != nil {
				return err
			}
			got, err := archiveMember(archive, file.name)
			if err != nil {
				return err
			}
			if !bytes.Equal(got, want) {
				return fmt.Errorf("stale skill archive: %s/%s", skill, file.name)
			}
		}
	}
	fmt.Fprintln(t.out, "Built CLI, Python wheel, npm package and Skill archives passed installation smoke checks")
	return nil
}
