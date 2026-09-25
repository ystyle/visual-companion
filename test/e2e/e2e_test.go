// Package e2e drives the real compiled binary over a real MCP stdio
// connection, the same way an agent host does.
//
// The in-package tests use an in-memory transport; this one is the only place
// that proves the shipped artifact works: cross-process JSON-RPC, embedded
// assets, the HTTP server, and the click round trip.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildBinary compiles the companion once per test run.
func buildBinary(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "visual-companion")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// connect launches the binary as a child process and speaks MCP to it.
func connect(t *testing.T, bin, projectDir string) *mcp.ClientSession {
	t.Helper()
	return connectWithRoots(t, bin, projectDir, nil)
}

// connectWithRoots launches the binary, optionally advertising MCP roots the
// way a service-shaped host (dsh, opencode) does. The child is started from a
// directory unrelated to the workspace, which is the situation that made
// process-startup path capture wrong.
func connectWithRoots(t *testing.T, bin, projectDir string, roots []string) *mcp.ClientSession {
	t.Helper()

	args := []string{}
	if projectDir != "" {
		args = append(args, "--project-dir", projectDir)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr
	cmd.Dir = t.TempDir() // deliberately NOT the workspace

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "test"},
		&mcp.ClientOptions{
			Capabilities: &mcp.ClientCapabilities{
				RootsV2: &mcp.RootCapabilities{ListChanged: true},
			},
		})
	if len(roots) > 0 {
		list := make([]*mcp.Root, 0, len(roots))
		for _, r := range roots {
			list = append(list, &mcp.Root{URI: fileURI(r)})
		}
		client.AddRoots(list...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to child process: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// fileURI renders a local path as a file:// URI.
func fileURI(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if runtime.GOOS == "windows" {
		u.Path = "/" + u.Path
	}
	return u.String()
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("call %s failed: %s", name, textOf(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s result: %v", name, err)
	}
	return out, textOf(res)
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestFullRoundTripOverStdio is the acceptance test for the shipped binary.
func TestFullRoundTripOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-spawn test in short mode")
	}
	bin := buildBinary(t)
	project := t.TempDir()
	cs := connect(t, bin, project)

	// 1. The agent starts the companion.
	start, text := call(t, cs, "start_companion", nil)
	url, _ := start["url"].(string)
	if url == "" {
		t.Fatalf("no url in %+v", start)
	}
	if !strings.Contains(text, url) {
		t.Errorf("the text the agent reads should contain the URL, got: %s", text)
	}
	t.Logf("companion url: %s", url)

	// 2. It pushes a named design.
	pushed, pushedText := call(t, cs, "push_screen", map[string]any{
		"html": `<h2>Which layout works better?</h2>
<div class="options">
  <div class="option" data-choice="a" onclick="toggleSelect(this)">
    <div class="letter">A</div><div class="content"><h3>Single Column</h3></div>
  </div>
  <div class="option" data-choice="b" onclick="toggleSelect(this)">
    <div class="letter">B</div><div class="content"><h3>Two Column</h3></div>
  </div>
</div>`,
		"design": "homepage-layout",
	})
	if pushed["design"] != "homepage-layout" || pushed["version"].(float64) != 1 {
		t.Fatalf("expected homepage-layout v1, got %+v", pushed)
	}
	if !strings.Contains(pushedText, "v1") {
		t.Errorf("the agent should be told the version, got: %s", pushedText)
	}
	screenPath, _ := pushed["screen"].(string)
	if _, err := os.Stat(screenPath); err != nil {
		t.Fatalf("screen file was not written: %v", err)
	}
	// The version is visible in the path, which is what makes the workspace
	// reviewable after the session ends.
	if !strings.HasSuffix(screenPath, filepath.Join("homepage-layout", "v1", "screen.html")) {
		t.Errorf("screen path %q does not encode design and version", screenPath)
	}

	// 3. The browser loads it (cookie carries the key, as after bootstrap).
	body := fetchAsBrowser(t, url)
	for _, want := range []string{
		"Which layout works better?", "id=\"frame-content\"", "vc-paused", "data-choice=\"b\"",
		"homepage-layout", `class="badge-version">v1<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("served page is missing %q", want)
		}
	}
	if strings.Count(body, "<html") != 1 {
		t.Error("fragment should be wrapped exactly once")
	}

	// 4. The user clicks.
	postClick(t, url, "b", "Two Column")

	// 5. The agent reads the click back.
	events, text := call(t, cs, "get_events", nil)
	if events["count"].(float64) != 1 {
		t.Fatalf("expected 1 event, got %+v", events)
	}
	if !strings.Contains(text, "Two Column") {
		t.Errorf("agent-facing text should name the choice, got: %s", text)
	}
	if !strings.Contains(text, "primary") {
		t.Errorf("agent-facing text should remind about the terminal reply, got: %s", text)
	}
	gen := events["generation"].(float64)
	if gen < 1 {
		t.Errorf("generation = %v, want >= 1", gen)
	}

	// 6. A fresh read is empty unless the agent asked to keep events.
	again, _ := call(t, cs, "get_events", nil)
	if again["count"].(float64) != 0 {
		t.Errorf("events should be cleared after a read, got %+v", again)
	}

	// 7. Revising the design publishes v2 rather than overwriting v1.
	second, _ := call(t, cs, "push_screen", map[string]any{
		"html": "<h2>Revised layout</h2>", "design": "homepage-layout",
	})
	if second["version"].(float64) != 2 {
		t.Fatalf("expected v2, got %+v", second)
	}
	if _, err := os.Stat(filepath.Join(project, ".visual-companion")); err != nil {
		t.Fatalf("mockups should live under .visual-companion: %v", err)
	}
	body = fetchAsBrowser(t, url)
	if !strings.Contains(body, `class="badge-version">v2<`) {
		t.Error("the browser should show v2 after the revision")
	}

	// 8. The session lives where it was asked to.
	wantDir := filepath.Join(project, ".visual-companion")
	if _, err := os.Stat(wantDir); err != nil {
		t.Errorf("sessions should live under the project dir: %v", err)
	}
	t.Logf("sessions stored in %s", wantDir)
}

// TestProcessExitReleasesThePort proves the MCP host's lifetime is what keeps
// the companion alive: no daemon, no pid file, nothing to clean up.
func TestProcessExitReleasesThePort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-spawn test in short mode")
	}
	bin := buildBinary(t)
	cs := connect(t, bin, t.TempDir())

	start, _ := call(t, cs, "start_companion", nil)
	url := start["url"].(string)
	base := url[:strings.Index(url, "/?key=")]

	if resp, err := http.Get(url); err != nil {
		t.Fatalf("companion not reachable while the server runs: %v", err)
	} else {
		resp.Body.Close()
	}

	if err := cs.Close(); err != nil {
		t.Logf("close: %v", err)
	}
	// Closing the MCP session closes stdin, which should end the process.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/")
		if err != nil {
			return // connection refused: the server is gone, as intended
		}
		resp.Body.Close()
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the HTTP server outlived the MCP connection")
}

// TestTwoSessionsCoexist checks that a second companion does not disturb the
// first, which an agent may do when the user opens a fresh brainstorm.
func TestTwoSessionsCoexist(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-spawn test in short mode")
	}
	bin := buildBinary(t)
	cs := connect(t, bin, t.TempDir())

	first, _ := call(t, cs, "start_companion", nil)
	second, _ := call(t, cs, "start_companion", nil)
	if first["url"] == second["url"] {
		t.Fatal("two sessions must not share a URL")
	}

	// push_screen with no session_id targets the most recent one.
	call(t, cs, "push_screen", map[string]any{"html": "<h2>Second session</h2>", "label": "s2"})
	if body := fetchAsBrowser(t, second["url"].(string)); !strings.Contains(body, "Second session") {
		t.Error("newest session did not receive the screen")
	}

	// The first session is still addressable by id.
	call(t, cs, "push_screen", map[string]any{
		"html": "<h2>First session</h2>", "label": "s1", "session_id": first["session_id"],
	})
	if body := fetchAsBrowser(t, first["url"].(string)); !strings.Contains(body, "First session") {
		t.Error("explicit session_id did not route the screen")
	}
}

// TestWorkspaceIsDiscoveredFromClientRoots is the acceptance test for
// service-shaped hosts: the server is launched with no project directory, from
// an unrelated working directory, and must still put its sessions in the
// workspace the client reports over MCP roots.
func TestWorkspaceIsDiscoveredFromClientRoots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-spawn test in short mode")
	}
	bin := buildBinary(t)
	workspace := t.TempDir()
	// A marker so the workspace is unmistakably a project.
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}

	cs := connectWithRoots(t, bin, "", []string{workspace})

	out, text := call(t, cs, "start_companion", nil)
	dir, _ := out["session_dir"].(string)
	wantRoot := filepath.Join(workspace, ".visual-companion")
	if !strings.HasPrefix(dir, wantRoot) {
		t.Fatalf("session_dir = %q, want it under %q\ntext was:\n%s", dir, wantRoot, text)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("session directory was not created: %v", err)
	}

	// The whole loop must work from a discovered workspace too.
	call(t, cs, "push_screen", map[string]any{"html": "<h2>Discovered</h2>", "label": "one"})
	if body := fetchAsBrowser(t, out["url"].(string)); !strings.Contains(body, "Discovered") {
		t.Error("screen not served in a roots-discovered session")
	}

	listed, listedText := call(t, cs, "list_sessions", nil)
	if listed["count"].(float64) != 1 {
		t.Fatalf("expected 1 session, got %+v", listed)
	}
	if !strings.Contains(listedText, wantRoot) {
		t.Errorf("list_sessions should name %q, got:\n%s", wantRoot, listedText)
	}
}

// TestProjectDirFlagBeatsRoots: an explicit pin must win over discovery.
func TestProjectDirFlagBeatsRoots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-spawn test in short mode")
	}
	bin := buildBinary(t)
	rootsWorkspace := t.TempDir()
	pinned := t.TempDir()

	cs := connectWithRoots(t, bin, pinned, []string{rootsWorkspace})

	out, _ := call(t, cs, "start_companion", nil)
	dir, _ := out["session_dir"].(string)
	if !strings.HasPrefix(dir, filepath.Join(pinned, ".visual-companion")) {
		t.Errorf("session_dir = %q, want it pinned under %q", dir, pinned)
	}
	if strings.Contains(dir, rootsWorkspace) {
		t.Errorf("roots overrode the explicit flag: %q", dir)
	}
}

// --- helpers ---------------------------------------------------------------

func fetchAsBrowser(t *testing.T, keyedURL string) string {
	t.Helper()
	i := strings.Index(keyedURL, "/?key=")
	if i < 0 {
		t.Fatalf("url %q has no key", keyedURL)
	}
	base, key := keyedURL[:i], keyedURL[i+len("/?key="):]

	req, _ := http.NewRequest("GET", base+"/", nil)
	req.AddCookie(&http.Cookie{Name: "companion-key", Value: key})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / returned %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func postClick(t *testing.T, keyedURL, choice, text string) {
	t.Helper()
	i := strings.Index(keyedURL, "/?key=")
	base, key := keyedURL[:i], keyedURL[i+len("/?key="):]

	payload := fmt.Sprintf(`{"type":"click","choice":%q,"text":%q}`, choice, text)
	resp, err := http.Post(base+"/events?key="+key, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /events returned %d", resp.StatusCode)
	}
}
