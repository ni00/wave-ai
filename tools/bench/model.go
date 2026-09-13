package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func expectedResponse(o options) string { return strings.Repeat("x", o.Chunks*o.ChunkBytes) }

func modelHandler(o options) http.Handler {
	// Pre-encode immutable chunks so JSON encoding does not dominate the mock.
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": strings.Repeat("x", o.ChunkBytes)}}}})
	chunk := fmt.Sprintf("data: %s\n\n", raw)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 2<<20)); err != nil {
			http.Error(w, "request too large", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for range o.Chunks {
			if err := pause(r.Context(), o.ModelDelay/time.Duration(o.Chunks)); err != nil {
				return
			}
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":16,\"completion_tokens\":%d}}\n\ndata: [DONE]\n\n", max(1, o.Chunks*o.ChunkBytes/4))
		flusher.Flush()
	})
}
