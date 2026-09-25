package companion

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const cookieName = "companion-key"

// Assets are the browser-side files, embedded into the binary at build time
// so the shipped artifact is a single self-contained file.
type Assets struct {
	Frame  []byte // full-page frame: theme, header, CSS component classes
	Helper []byte // client script: click capture, status, reconnect handling
}

// routes wires every HTTP endpoint.
func (s *Session) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.handleScreen)
	mux.HandleFunc("GET /events", s.handleSSE)
	mux.HandleFunc("POST /events", s.handlePostEvent)
	mux.HandleFunc("GET /files/{name}", s.handleFile)
	mux.HandleFunc("/", s.handleNotFound)
}

// guard authorizes the request and attaches the session cookie. It writes the
// 403 response itself and reports whether the caller should continue.
func (s *Session) guard(w http.ResponseWriter, r *http.Request) bool {
	if !s.authorized(r) {
		writeSecurityHeaders(w.Header())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, forbiddenPage)
		return false
	}
	// Mirror the key into a cookie so same-origin subresources and the SSE
	// stream authenticate without repeating it. HttpOnly keeps it away from
	// page scripts.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    s.key,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return true
}

// authorized accepts the session key either as ?key= or as the cookie. Both
// comparisons are constant time.
func (s *Session) authorized(r *http.Request) bool {
	if given := r.URL.Query().Get("key"); given != "" {
		return constantTimeEqual(given, s.key)
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return constantTimeEqual(c.Value, s.key)
	}
	return false
}

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeSecurityHeaders(h http.Header) {
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// inline script/style are required: the helper is injected into the page
	// and the frame carries its theme in a <style> block.
	h.Set("Content-Security-Policy",
		"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; "+
			"img-src data: https:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
}

// --- GET / -----------------------------------------------------------------

func (s *Session) handleScreen(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	// A keyed first load bootstraps the cookie in sessionStorage, then returns
	// to a bare "/" so the address bar carries no secret.
	if given := r.URL.Query().Get("key"); given != "" && constantTimeEqual(given, s.key) {
		writeSecurityHeaders(w.Header())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, bootstrapPage(s.key))
		return
	}

	screen := s.currentScreen()
	entry := s.currentEntry()
	body := waitingPage
	if screen != "" {
		if raw, err := os.ReadFile(screen); err == nil {
			body = s.composeScreen(string(raw), entry)
		}
	}
	writeSecurityHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, body)
}

// composeScreen turns an agent-authored file into a servable page: fragments
// get wrapped in the frame, full documents are passed through. Either way the
// helper script is injected.
func (s *Session) composeScreen(raw string, entry screenEntry) string {
	var html string
	if isFullDocument(raw) {
		html = raw
		// A full document is the agent's own page: it has no frame header to
		// put a badge in, so float one instead of restructuring their markup.
		if entry.design != "" {
			if idx := strings.LastIndex(html, "</body>"); idx >= 0 {
				html = html[:idx] + floatingBadge(entry) + "\n" + html[idx:]
			} else {
				html += floatingBadge(entry)
			}
		}
	} else {
		html = strings.Replace(renderFrame(s.assets.Frame), "<!-- CONTENT -->", raw, 1)
	}
	html = strings.Replace(html, "<!-- BADGE -->", badgeMarkup(entry), 1)

	injection := "<script>\n" + string(s.assets.Helper) + "\n</script>"
	if idx := strings.LastIndex(html, "</body>"); idx >= 0 {
		return html[:idx] + injection + "\n" + html[idx:]
	}
	return html + injection
}

// badgeMarkup renders the design/version chip shown in the frame header. An
// empty string leaves the header badge hidden.
func badgeMarkup(entry screenEntry) string {
	if entry.design == "" {
		return ""
	}
	return `<span class="badge-design">` + htmlText(entry.design) + `</span>` +
		`<span class="badge-version">v` + strconv.Itoa(entry.version) + `</span>` +
		`<style>.header .badge{display:flex}</style>`
}

// floatingBadge is the same information for a full-document screen, styled
// inline so it works without the frame's CSS.
func floatingBadge(entry screenEntry) string {
	return `<div style="position:fixed;left:1rem;bottom:1rem;z-index:99998;display:flex;` +
		`align-items:center;gap:.4rem;font-family:system-ui,sans-serif;font-size:.75rem;` +
		`background:rgba(0,0,0,.72);color:#fff;padding:.35rem .6rem;border-radius:999px">` +
		`<span style="font-weight:600">` + htmlText(entry.design) + `</span>` +
		`<span style="opacity:.75">v` + strconv.Itoa(entry.version) + `</span></div>`
}

// isFullDocument reports whether the agent supplied a complete page rather
// than a fragment. Fragments are the default and get the frame's CSS.
func isFullDocument(html string) bool {
	trimmed := strings.ToLower(strings.TrimLeft(html, " \t\r\n"))
	return strings.HasPrefix(trimmed, "<!doctype") || strings.HasPrefix(trimmed, "<html")
}

// assets is set once at construction.
func (s *Session) SetAssets(a Assets) { s.assets = a }

// --- GET /events (SSE) -----------------------------------------------------

func (s *Session) handleSSE(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	writeSecurityHeaders(h)
	h.Set("Content-Type", "text/event-stream")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // defeat proxy buffering
	w.WriteHeader(http.StatusOK)

	msgs, cancel := s.bus.subscribe()
	defer cancel()

	fmt.Fprintf(w, "event: hello\ndata: {\"generation\":%d}\n\n", s.Generation())
	flusher.Flush()

	interval := s.heartbeat
	if interval <= 0 {
		interval = heartbeatInterval
	}
	keepalive := time.NewTicker(interval)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case msg, open := <-msgs:
			if !open {
				return
			}
			payload, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-keepalive.C:
			// Comment frame: keeps intermediaries from closing an idle
			// connection, and proves liveness to the browser.
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// --- POST /events ----------------------------------------------------------

func (s *Session) handlePostEvent(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if ev.Type == "" {
		ev.Type = "click"
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	if err := s.recordEvent(ev); err != nil {
		http.Error(w, "cannot record event", http.StatusInternalServerError)
		return
	}
	writeSecurityHeaders(w.Header())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, `{"status":"recorded"}`)
}

// --- GET /files/{name} -----------------------------------------------------

func (s *Session) handleFile(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	name := path.Base(r.PathValue("name"))
	if name == "" || name == "." || name == "/" || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.ContentDir(), name)
	// Refuse symlinks and anything that resolves outside the content dir.
	li, err := os.Lstat(full)
	if err != nil || !li.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	realContent, err1 := filepath.EvalSymlinks(s.ContentDir())
	realFile, err2 := filepath.EvalSymlinks(full)
	if err1 != nil || err2 != nil || !strings.HasPrefix(realFile, realContent+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(realFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeSecurityHeaders(w.Header())
	w.Header().Set("Content-Type", mimeType(name))
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	_, _ = io.Copy(w, f)
}

func mimeType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func (s *Session) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeSecurityHeaders(w.Header())
	http.NotFound(w, r)
}

// --- static pages ----------------------------------------------------------

const forbiddenPage = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Session key required</title>
<style>body{font-family:system-ui,sans-serif;padding:2rem;max-width:800px;margin:0 auto}
h1{color:#333}p{color:#666}code{background:#f0f0f0;padding:.1em .3em;border-radius:4px}</style>
</head><body><h1>Session key required</h1>
<p>This page needs the full URL your coding agent gave you, including the
<code>?key=&hellip;</code> part. Copy the complete URL and open it again.</p></body></html>`

const waitingPage = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Brainstorm Companion</title>
<style>body{font-family:system-ui,sans-serif;padding:2rem;max-width:800px;margin:0 auto}
h1{color:#333}p{color:#666}</style>
</head><body><h1>Brainstorm Companion</h1>
<p>Waiting for the agent to push a screen&hellip;</p></body></html>`

func bootstrapPage(key string) string {
	encoded, _ := json.Marshal(key)
	return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Opening Brainstorm Companion</title></head>
<body><script>
try { sessionStorage.setItem('brainstorm-session-key', ` + string(encoded) + `); } catch (e) {}
location.replace('/');
</script></body></html>`
}

// renderFrame clears the branding marker. This build carries no third-party
// branding or telemetry; replacing the marker keeps the template
// upstream-compatible.
func renderFrame(frame []byte) string {
	return strings.Replace(string(frame), "<!-- BRANDING -->", "", 1)
}

// htmlText escapes a value for insertion into HTML text content.
func htmlText(v string) string {
	return html.EscapeString(v)
}
