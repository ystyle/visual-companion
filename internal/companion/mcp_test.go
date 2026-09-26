package companion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestPair wires an in-memory MCP client to a real server with the
// companion tools registered — the same path a host uses, minus the process
// boundary. The registry is pinned to a temp dir so these tests do not depend
// on workspace discovery; TestWorkspaceComesFromClientRoots covers that.
func newTestPair(t *testing.T) (*mcp.ClientSession, *Registry) {
	t.Helper()
	return newPair(t, t.TempDir(), nil)
}

// newPair builds the client/server pair with an explicit session dir and an
// optional set of roots to advertise to the server.
func newPair(t *testing.T, sessionDir string, roots []string) (*mcp.ClientSession, *Registry) {
	t.Helper()

	reg := NewRegistry(testAssets(), sessionDir, "127.0.0.1", "", false)
	t.Cleanup(reg.CloseAll)

	server := mcp.NewServer(&mcp.Implementation{Name: "visual-companion-test", Version: "test"}, nil)
	RegisterTools(server, reg, "test")

	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}

	// A real host advertises roots with listChanged; declare it explicitly so
	// the shape matches what dsh/opencode send rather than relying on the
	// SDK's historical default.
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"},
		&mcp.ClientOptions{
			Capabilities: &mcp.ClientCapabilities{
				RootsV2: &mcp.RootCapabilities{ListChanged: true},
			},
		})
	if len(roots) > 0 {
		list := make([]*mcp.Root, 0, len(roots))
		for _, r := range roots {
			list = append(list, &mcp.Root{URI: pathToFileURI(r), Name: filepath.Base(r)})
		}
		client.AddRoots(list...)
	}

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	return cs, reg
}

// pathToFileURI converts a local path to a file:// URI the way clients do.
func pathToFileURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if runtime.GOOS == "windows" {
		u.Path = "/" + u.Path
	}
	return u.String()
}

// callTool invokes a tool and returns its structured content as a map.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	out, _ := callToolText(t, cs, name, args)
	return out
}

// callToolText also returns the human-readable text the agent would read.
func callToolText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("call %s returned an error result: %s", name, contentText(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content for %s: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content for %s: %v", name, err)
	}
	return out, contentText(res)
}

// contentText flattens a result's content blocks into readable text.
func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsAreRegistered(t *testing.T) {
	cs, _ := newTestPair(t)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %s has no description; the agent cannot use it blind", tool.Name)
		}
	}
	for _, want := range []string{"start_companion", "push_screen", "get_events"} {
		if !got[want] {
			t.Errorf("tool %s is not registered", want)
		}
	}
}

func TestStartCompanionReturnsUsableURL(t *testing.T) {
	cs, _ := newTestPair(t)

	out := callTool(t, cs, "start_companion", nil)
	url, _ := out["url"].(string)
	if url == "" {
		t.Fatalf("start_companion returned no url: %+v", out)
	}
	if !strings.Contains(url, "?key=") {
		t.Errorf("url %q is missing the session key", url)
	}

	// The keyed URL must actually serve the companion.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("keyed URL returned %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "brainstorm-session-key") {
		t.Errorf("keyed first load should bootstrap the session key, got: %.200s", body)
	}
}

func TestCallingToolsWithoutASessionExplainsItself(t *testing.T) {
	cs, _ := newTestPair(t)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_events",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result when no session is running")
	}
	if text := contentText(res); !strings.Contains(text, "start_companion") {
		t.Errorf("error should tell the agent what to do instead, got: %s", text)
	}
}

func TestScreenIsWrappedAndServed(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)

	callTool(t, cs, "push_screen", map[string]any{
		"html":  `<h2>Which layout?</h2><div class="options"><div class="option" data-choice="a">A</div></div>`,
		"label": "layout",
	})

	body := fetchScreen(t, url)
	if !strings.Contains(body, "Which layout?") {
		t.Error("screen content is not being served")
	}
	if !strings.Contains(body, "id=\"frame-content\"") {
		t.Error("fragment was not wrapped in the frame template")
	}
	if !strings.Contains(body, "vc-paused") {
		t.Error("helper script was not injected")
	}
	if n := strings.Count(body, "<html"); n != 1 {
		t.Errorf("expected exactly one <html> element, found %d", n)
	}
}

func TestFullDocumentIsNotWrapped(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)

	callTool(t, cs, "push_screen", map[string]any{
		"html":  "<!DOCTYPE html><html><body><h1>Custom</h1></body></html>",
		"label": "custom",
	})

	body := fetchScreen(t, url)
	if !strings.Contains(body, "<h1>Custom</h1>") {
		t.Fatal("full document content missing")
	}
	if strings.Contains(body, "id=\"frame-content\"") {
		t.Error("a full document must not be wrapped in the frame")
	}
	if !strings.Contains(body, "vc-paused") {
		t.Error("helper must still be injected into a full document")
	}
}

func TestClickRoundTrip(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)

	callTool(t, cs, "push_screen", map[string]any{
		"html":  `<div class="options"><div class="option" data-choice="a">Single Column</div></div>`,
		"label": "layout",
	})

	// The user has not clicked yet.
	out, text := callToolText(t, cs, "get_events", nil)
	if out["count"].(float64) != 0 {
		t.Fatalf("expected no events before any click, got %+v", out)
	}
	// The text the agent reads must steer it back to the terminal reply.
	for _, want := range []string{"terminal", "primary"} {
		if !strings.Contains(text, want) {
			t.Errorf("empty-events text should mention %q, got: %s", want, text)
		}
	}

	// Simulate two clicks from the browser, in order.
	postClick(t, url, "a", "Single Column")
	time.Sleep(20 * time.Millisecond)
	postClick(t, url, "b", "Two Column")

	out = callTool(t, cs, "get_events", nil)
	if out["count"].(float64) != 2 {
		t.Fatalf("expected 2 events, got %+v", out)
	}
	events, _ := out["events"].([]any)
	first, _ := events[0].(map[string]any)
	last, _ := events[1].(map[string]any)
	if first["choice"] != "a" || last["choice"] != "b" {
		t.Errorf("events out of order: %+v", events)
	}
	if last["text"] != "Two Column" {
		t.Errorf("event text not preserved: %+v", last)
	}
	if out["generation"].(float64) < 1 {
		t.Errorf("generation should have advanced past 0, got %v", out["generation"])
	}
}

func TestEventsClearOnReadAndOnNewScreen(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)

	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>One</h2>", "label": "one"})
	postClick(t, url, "a", "A")

	// keep=true reads without clearing, so a re-read sees the same click.
	kept := callTool(t, cs, "get_events", map[string]any{"keep": true})
	if kept["count"].(float64) != 1 {
		t.Fatalf("keep=true should not clear, got %+v", kept)
	}
	again := callTool(t, cs, "get_events", map[string]any{"keep": true})
	if again["count"].(float64) != 1 {
		t.Fatalf("keep=true second read should still see the click, got %+v", again)
	}

	// A plain read clears.
	_ = callTool(t, cs, "get_events", nil)
	afterClear := callTool(t, cs, "get_events", nil)
	if afterClear["count"].(float64) != 0 {
		t.Fatalf("expected the default read to clear events, got %+v", afterClear)
	}

	// Pushing a new screen also resets the interaction round.
	postClick(t, url, "a", "A")
	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>Two</h2>", "label": "two"})
	time.Sleep(watchInterval * 3)
	fresh := callTool(t, cs, "get_events", nil)
	if fresh["count"].(float64) != 0 {
		t.Errorf("a new screen must start a fresh round of events, got %+v", fresh)
	}
}

func TestScreensGetDistinctFilenames(t *testing.T) {
	cs, reg := newTestPair(t)
	callTool(t, cs, "start_companion", nil)

	var paths []string
	for i := 0; i < 3; i++ {
		out := callTool(t, cs, "push_screen", map[string]any{
			"html":  fmt.Sprintf("<h2>Iteration %d</h2>", i),
			"label": "layout",
		})
		paths = append(paths, out["screen"].(string))
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("screen filename reused: %s (the browser picks the newest file by mtime)", p)
		}
		seen[p] = true
	}

	s, err := reg.Resolve("")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// The last screen pushed must be the one being served.
	if got := s.currentScreen(); got != paths[len(paths)-1] {
		t.Errorf("newest screen is %s, want %s", got, paths[len(paths)-1])
	}
}

func TestPushRejectsEmptyHTML(t *testing.T) {
	cs, _ := newTestPair(t)
	callTool(t, cs, "start_companion", nil)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "push_screen",
		Arguments: map[string]any{"html": "   "},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result for empty html")
	}
}

func TestUnknownSessionIDIsReported(t *testing.T) {
	cs, _ := newTestPair(t)
	callTool(t, cs, "start_companion", nil)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "push_screen",
		Arguments: map[string]any{"html": "<h2>x</h2>", "session_id": "nope"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Error("expected an error result for an unknown session_id")
	}
}

// --- helpers ---------------------------------------------------------------

// fetchScreen loads the companion the way an open browser tab does: a bare "/"
// carrying the session cookie.
func fetchScreen(t *testing.T, keyedURL string) string {
	t.Helper()
	base, key := splitKeyedURL(t, keyedURL)
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: key})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s/: %v", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / returned %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func splitKeyedURL(t *testing.T, keyedURL string) (base, key string) {
	t.Helper()
	i := strings.Index(keyedURL, "/?key=")
	if i < 0 {
		t.Fatalf("url %q has no ?key=", keyedURL)
	}
	return keyedURL[:i], keyedURL[i+len("/?key="):]
}

func postClick(t *testing.T, keyedURL, choice, text string) {
	t.Helper()
	base, key := splitKeyedURL(t, keyedURL)
	payload := fmt.Sprintf(`{"type":"click","choice":%q,"text":%q}`, choice, text)
	resp, err := http.Post(base+"/events?key="+key, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST event: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST event returned %d", resp.StatusCode)
	}
}
