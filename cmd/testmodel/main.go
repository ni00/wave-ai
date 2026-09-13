// Command testmodel is a deterministic Chat Completions server for local demos.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:9101"
	}
	http.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		hasBash, hasResult := false, false
		for _, t := range in.Tools {
			if t.Function.Name == "bash" {
				hasBash = true
			}
		}
		for _, m := range in.Messages {
			if m.Role == "tool" {
				hasResult = true
			}
		}
		delta := map[string]any{"content": "Task complete."}
		reason := "stop"
		if hasBash && !hasResult {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "demo-bash", "type": "function", "function": map[string]any{"name": "bash", "arguments": `{"command":"printf 'hello from wave' > /mnt/session/outputs/report.txt"}`}}}}
			reason = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		frame := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5}}
		raw, _ := json.Marshal(frame)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
	})
	log.Fatal(http.ListenAndServe(addr, nil))
}
