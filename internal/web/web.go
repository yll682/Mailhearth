// Package web embeds the compiled single-page application and serves it
// with sensible caching: hashed assets are immutable, index.html is not.
package web

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
)

//go:embed all:dist
var dist embed.FS

type asset struct {
	body  []byte
	gz    []byte
	ctype string
}

// Handler serves the SPA. Unknown paths fall back to index.html so client
// routing works on refresh.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	cache := map[string]*asset{}
	var mu sync.RWMutex
	load := func(p string) *asset {
		mu.RLock()
		a := cache[p]
		mu.RUnlock()
		if a != nil {
			return a
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return nil
		}
		a = &asset{body: b, ctype: contentType(p)}
		if len(b) > 1024 && compressible(a.ctype) {
			var buf bytes.Buffer
			gw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			gw.Write(b)
			gw.Close()
			if buf.Len() < len(b) {
				a.gz = buf.Bytes()
			}
		}
		mu.Lock()
		cache[p] = a
		mu.Unlock()
		return a
	}
	index := load("index.html")
	if index == nil {
		// The binary was built without running the web build. Serve a page that
		// says so instead of a bare 404, and keep the API fully usable.
		index = &asset{body: []byte(notBuiltPage), ctype: "text/html; charset=utf-8"}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		var a *asset
		immutable := false
		if p != "" && p != "index.html" {
			a = load(p)
			immutable = strings.HasPrefix(p, "assets/")
		}
		if a == nil {
			a = index
		}
		h := w.Header()
		h.Set("Content-Type", a.ctype)
		if immutable {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		h.Set("Vary", "Accept-Encoding")
		body := a.body
		if a.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.Set("Content-Encoding", "gzip")
			body = a.gz
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			w.Write(body)
		}
	})
}

func contentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".webmanifest":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".txt":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

func compressible(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") || strings.Contains(ct, "json") || strings.Contains(ct, "svg")
}

// notBuiltPage is served when the binary was compiled without the web assets
// (a plain `go build` in a fresh checkout). The JSON API still works.
const notBuiltPage = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Mailhearth</title>
<style>body{font:15px/1.6 system-ui,sans-serif;max-width:40rem;margin:12vh auto;padding:0 1.5rem;color:#1f1b17;background:#f6f3ee}
code{background:#efeae3;padding:.15em .4em;border-radius:4px;font-size:.9em}h1{font-size:1.4rem}</style></head>
<body><h1>Mailhearth is running, but the web client is not built</h1>
<p>This binary was compiled without the single-page app. Build it and recompile:</p>
<pre><code>cd web &amp;&amp; npm ci &amp;&amp; npm run build
go build ./cmd/mailhearth</code></pre>
<p>Or use <code>make build</code>, which does both. The JSON API under <code>/api</code> is unaffected.</p>
</body></html>`
