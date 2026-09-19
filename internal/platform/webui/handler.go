// Package webui serves the prebuilt console from the Wave binary.
package webui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"wave-ai.local/wave/web"
)

// asset holds both encodings of a console file together with their ETags so a
// request never repeats decompression or hashing work. Assets are immutable
// (embedded at build time), so entries are computed once on first use.
type asset struct {
	gzip      []byte
	plain     []byte
	etagGzip  string
	etagPlain string
}

var errInvalidAsset = errors.New("invalid embedded asset")

var (
	assetMu    sync.RWMutex
	assetCache = map[string]*asset{}
)

func loadAsset(name string) (*asset, error) {
	assetMu.RLock()
	a := assetCache[name]
	assetMu.RUnlock()
	if a != nil {
		return a, nil
	}
	gz, err := web.ReadAsset(name + ".gz")
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, errInvalidAsset
	}
	plain, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return nil, errInvalidAsset
	}
	a = &asset{
		gzip:      gz,
		plain:     plain,
		etagGzip:  fmt.Sprintf(`"%x"`, sha256.Sum256(gz)),
		etagPlain: fmt.Sprintf(`"%x"`, sha256.Sum256(plain)),
	}
	assetMu.Lock()
	assetCache[name] = a
	assetMu.Unlock()
	return a, nil
}

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
	a, err := loadAsset(name)
	if err != nil {
		if errors.Is(err, errInvalidAsset) {
			http.Error(w, "invalid embedded asset", 500)
			return
		}
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
	data, etag := a.plain, a.etagPlain
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.TrimSpace(part) == "gzip" {
			data, etag = a.gzip, a.etagGzip
			w.Header().Set("Content-Encoding", "gzip")
		}
	}
	w.Header().Set("ETag", etag)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
