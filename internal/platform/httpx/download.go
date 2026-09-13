package httpx

import (
	"io"
	"mime"
	"net/http"
	"time"
)

// Download serves untrusted bytes as an attachment after business authorization.
func Download(w http.ResponseWriter, r *http.Request, name string, modified time.Time, body io.ReadSeeker) {
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, modified, body)
}
