package companion

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnauthorizedRequestsAreRejected(t *testing.T) {
	s := startTestSession(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", s.Port())

	cases := []struct {
		name string
		url  string
	}{
		{"no key", base + "/"},
		{"wrong key", base + "/?key=deadbeef"},
		{"events without key", base + "/events"},
		{"file without key", base + "/files/anything.html"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(tc.url)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.url, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("got %d, want 403", resp.StatusCode)
			}
		})
	}
}

func TestPostEventRequiresKey(t *testing.T) {
	s := startTestSession(t)
	url := fmt.Sprintf("http://127.0.0.1:%d/events", s.Port())

	resp, err := http.Post(url, "application/json", strings.NewReader(`{"type":"click","choice":"a"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("got %d, want 403", resp.StatusCode)
	}
	if events, _ := s.ReadEvents(true); len(events) != 0 {
		t.Error("an unauthorized POST must not record an event")
	}
}

func TestKeyIsMirroredIntoCookie(t *testing.T) {
	s := startTestSession(t)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/?key=%s", s.Port(), s.Key()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatal("session cookie was not set")
	}
	if !found.HttpOnly {
		t.Error("cookie should be HttpOnly so page scripts cannot read the key")
	}
	if found.SameSite != http.SameSiteStrictMode {
		t.Error("cookie should be SameSite=Strict")
	}
}

func TestCookieAloneAuthenticates(t *testing.T) {
	s := startTestSession(t)
	writeScreen(t, s, "a", "<h2>Cookie authorised</h2>")
	time.Sleep(watchInterval * 2)

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", s.Port()), nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: s.Key()})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Cookie authorised") {
		t.Error("cookie-authenticated request did not reach the screen")
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	s := startTestSession(t)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/?key=%s", s.Port(), s.Key()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	want := map[string]string{
		"Referrer-Policy":              "no-referrer",
		"Cache-Control":                "no-store",
		"X-Frame-Options":              "DENY",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"frame-ancestors 'none'", "connect-src 'self'", "default-src 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing %q: %s", directive, csp)
		}
	}
}

func TestPathTraversalIsBlocked(t *testing.T) {
	s := startTestSession(t)
	writeScreen(t, s, "real", "<h2>ok</h2>")
	base := fmt.Sprintf("http://127.0.0.1:%d", s.Port())

	// A file outside the content dir that an escape would reach.
	secret := filepath.Join(s.Dir, "state", "secret.txt")
	if err := os.WriteFile(secret, []byte("sensitive"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	for _, p := range []string{
		"/files/..%2fstate%2fsecret.txt",
		"/files/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/files/.hidden",
		"/files/",
	} {
		resp, err := http.Get(base + p + "?key=" + s.Key())
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "sensitive") {
			t.Errorf("%s leaked a file outside the content dir", p)
		}
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s returned 200; expected a rejection", p)
		}
	}
}

func TestSymlinksOutsideContentDirAreRefused(t *testing.T) {
	s := startTestSession(t)
	secret := filepath.Join(s.Dir, "state", "secret.txt")
	if err := os.WriteFile(secret, []byte("sensitive"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	link := filepath.Join(s.ContentDir(), "escape.html")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/files/escape.html?key=%s", s.Port(), s.Key()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "sensitive") {
		t.Fatal("symlink escaped the content directory")
	}
}

func TestSSEStreamsReloadOnNewScreen(t *testing.T) {
	s := startTestSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://127.0.0.1:%d/events?key=%s", s.Port(), s.Key()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	reader := bufio.NewReader(resp.Body)

	// The stream opens with a hello frame.
	if ev, data := readSSEFrame(t, reader); ev != "hello" {
		t.Fatalf("first frame = %q, want hello", ev)
	} else if !strings.Contains(data, "generation") {
		t.Errorf("hello frame should carry the generation: %s", data)
	}

	// A new screen must produce a reload.
	writeScreen(t, s, "layout", "<h2>New</h2>")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ev, data := readSSEFrame(t, reader)
		if ev != "message" {
			continue
		}
		var msg busMsg
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			t.Fatalf("bad message payload %q: %v", data, err)
		}
		if msg.Type == "reload" {
			if msg.Generation < 1 {
				t.Errorf("reload generation = %d, want >= 1", msg.Generation)
			}
			return
		}
	}
	t.Fatal("no reload frame arrived after a new screen was pushed")
}

func TestSSEHeartbeatKeepsStreamAlive(t *testing.T) {
	s := startTestSession(t)
	// Shorten the heartbeat so the test does not idle for 15 seconds.
	s.heartbeat = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://127.0.0.1:%d/events?key=%s", s.Port(), s.Key()), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	out := make(chan string, 1)
	go func() {
		// A heartbeat is a comment frame, so it never completes an SSE event.
		// Read raw lines and look for it rather than parsing frames.
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "keepalive") {
				out <- "keepalive"
				return
			}
		}
		out <- ""
	}()

	select {
	case got := <-out:
		if got != "keepalive" {
			t.Fatalf("stream closed before a heartbeat: %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no heartbeat arrived; intermediaries would drop this stream")
	}
}

func TestOldEventsAreDroppedWhenANewScreenArrives(t *testing.T) {
	s := startTestSession(t)
	writeScreen(t, s, "first", "<h2>First</h2>")
	time.Sleep(watchInterval * 2)

	if err := s.recordEvent(Event{Type: "click", Choice: "a", Text: "First choice"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if events, _ := s.ReadEvents(true); len(events) != 1 {
		t.Fatalf("expected the click to be stored, got %d", len(events))
	}

	writeScreen(t, s, "second", "<h2>Second</h2>")
	time.Sleep(watchInterval * 3)

	events, err := s.ReadEvents(true)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("clicks from the previous screen leaked into the new one: %+v", events)
	}
}

func TestWaitingPageBeforeAnyScreen(t *testing.T) {
	s := startTestSession(t)
	body := getAsUser(t, s, "/")
	if !strings.Contains(body, "Waiting for the agent") {
		t.Errorf("expected the waiting page, got: %.200s", body)
	}
}

func TestStaticFilesServeImages(t *testing.T) {
	s := startTestSession(t)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(filepath.Join(s.ContentDir(), "shot.png"), png, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/files/shot.png?key=%s", s.Port(), s.Key()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
}

func TestUnknownRoutesAre404(t *testing.T) {
	s := startTestSession(t)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/nope?key=%s", s.Port(), s.Key()))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got %d, want 404", resp.StatusCode)
	}
}

// --- helpers ---------------------------------------------------------------

// readSSEFrame reads one `event:`/`data:` pair, skipping comment heartbeats.
func readSSEFrame(t *testing.T, r *bufio.Reader) (event, data string) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "":
			if event != "" || data != "" {
				return event, data
			}
		}
	}
}

// writeScreen publishes a screen the way push_screen does: a new version of a
// design. The name doubles as the design name, so repeated calls with the same
// name produce v1, v2, ...
func writeScreen(t *testing.T, s *Session, name, html string) string {
	t.Helper()
	entry, err := s.nextScreen(strings.TrimSuffix(name, ".html"))
	if err != nil {
		t.Fatalf("nextScreen %s: %v", name, err)
	}
	if err := os.WriteFile(entry.path, []byte(html), 0o644); err != nil {
		t.Fatalf("write screen %s: %v", name, err)
	}
	return entry.path
}

func getKeyed(t *testing.T, s *Session, path string) string {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s%skey=%s", s.Port(), path, sep, s.Key()))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// getAsUser mimics what the browser ends up doing: the keyed first load hands
// the key to the page, which then returns to a bare "/" with a cookie. Tests
// that assert on served content must go through the cookie, not the keyed URL.
func getAsUser(t *testing.T, s *Session, path string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", s.Port(), path), nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: s.Key()})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}
