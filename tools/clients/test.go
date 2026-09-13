package main

import (
	"context"
	"fmt"
	"net/http/httptest"

	"wave-ai.local/wave/tests/clients/fixture"
)

func (t *tool) test(ctx context.Context) error {
	handler, err := fixture.New(t.path("tests/clients/fixtures.json"))
	if err != nil {
		return err
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	env := map[string]string{"WAVE_CLIENT_TEST_URL": server.URL}
	suites := []struct {
		dir  string
		args []string
	}{
		{"sdks/go", []string{"go", "test", "-race", "./..."}},
		{"sdks/python", []string{"uv", "run", "--frozen", "pytest", "-q"}},
		{"sdks/typescript", []string{"npm", "test"}},
		{"cli", []string{"go", "test", "-race", "./..."}},
	}
	for _, suite := range suites {
		fmt.Fprintln(t.out, "Running", suite.dir)
		if err = t.run(ctx, t.path(suite.dir), env, suite.args...); err != nil {
			return err
		}
	}
	return nil
}
