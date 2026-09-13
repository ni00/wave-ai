// Isolated real-service fixture. Never point WAVE_TEST_DATABASE_URL at live data.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"wave-ai.local/wave/internal/app"
	"wave-ai.local/wave/internal/platform/auth"
	"wave-ai.local/wave/internal/platform/config"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if os.Getenv("WAVE_TEST_DATABASE_URL") == "" || len(os.Args) != 2 {
		return fmt.Errorf("isolated test database and private connection-file path required")
	}
	gin.SetMode(gin.ReleaseMode)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
			http.Error(w, "bad JSON", 400)
			return
		}
		done := false
		for _, m := range request.Messages {
			done = done || m.Role == "tool"
		}
		delta := map[string]any{"content": "completed"}
		reason := "stop"
		if !done {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "client-call", "type": "function", "function": map[string]any{"name": "client_echo", "arguments": `{"message":"hello"}`}}}}
			reason = "tool_calls"
		}
		frame := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 1}}
		data, _ := json.Marshal(frame)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}))
	defer model.Close()
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	cfg.DatabaseURL = os.Getenv("WAVE_TEST_DATABASE_URL")
	cfg.Role = "worker"
	cfg.ModelBaseURL = model.URL
	cfg.ModelAPIKey = ""
	cfg.WorkerConcurrency = 2
	cfg.LeaseSeconds = 5
	cfg.ModelTimeoutSec = 10
	if e = app.Init(ctx, cfg.DatabaseURL); e != nil {
		return e
	}
	service, e := app.New(ctx, cfg)
	if e != nil {
		return e
	}
	defer service.Close()
	key, e := auth.Bootstrap(ctx, service.DB, "client-tests", "owner", "test", auth.ScopeAPI)
	if e != nil {
		return e
	}
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	data, _ := json.Marshal(map[string]string{"url": server.URL, "key": key})
	if e = os.WriteFile(os.Args[1], data, 0600); e != nil {
		return e
	}
	return service.Run(ctx)
}
