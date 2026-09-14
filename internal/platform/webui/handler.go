// Package webui serves the prebuilt console from the Wave binary.
package webui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

//go:embed static
var assets embed.FS

func Register(r *gin.Engine) {
	h := http.HandlerFunc(serve)
	r.GET("/console/*path", gin.WrapH(h))
	r.HEAD("/console/*path", gin.WrapH(h))
}
func serve(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/console/")
	if name == "" {
		name = "index.html"
	}
	if strings.Contains(name, "..") || strings.Contains(name, "\\") {
		http.NotFound(w, r)
		return
	}
	data, err := assets.ReadFile("static/" + name + ".gz")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	compressed := false
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.TrimSpace(part) == "gzip" {
			compressed = true
		}
	}
	if compressed {
		w.Header().Set("Content-Encoding", "gzip")
	} else {
		reader, e := gzip.NewReader(bytes.NewReader(data))
		if e != nil {
			http.Error(w, "invalid embedded asset", 500)
			return
		}
		data, err = io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			http.Error(w, "invalid embedded asset", 500)
			return
		}
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(data)))
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
