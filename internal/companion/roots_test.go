package companion

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWorkspaceComesFromClientRoots is the reason this design exists: a
// service-shaped host boots the MCP server before any workspace exists, so the
// session must land in the workspace the client reports, not in whatever
// directory the process happened to start in.
func TestWorkspaceComesFromClientRoots(t *testing.T) {
	workspace := t.TempDir()
	// Give it a project marker so it is unambiguously a workspace.
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}

	cs, _ := newPair(t, "", []string{workspace})
	out, text := callToolText(t, cs, "start_companion", nil)

	dir, _ := out["session_dir"].(string)
	wantRoot := canonical(t, filepath.Join(workspace, sessionDirName))
	if !strings.HasPrefix(canonical(t, dir), wantRoot) {
		t.Errorf("session_dir = %q, want it under %q", dir, wantRoot)
	}
	if !strings.Contains(text, workspace) {
		t.Errorf("the agent should be told which workspace was used, got:\n%s", text)
	}
}

// TestFallbackToWorkingDirectory covers hosts that do not implement roots.
func TestFallbackToWorkingDirectory(t *testing.T) {
	workspace := t.TempDir()
	restore := chdir(t, workspace)
	defer restore()

	cs, _ := newPair(t, "", nil) // client advertises roots but supplies none
	out, text := callToolText(t, cs, "start_companion", nil)

	dir, _ := out["session_dir"].(string)
	wantRoot := canonical(t, filepath.Join(workspace, sessionDirName))
	if !strings.HasPrefix(canonical(t, dir), wantRoot) {
		t.Errorf("session_dir = %q, want it under %q", dir, wantRoot)
	}
	if !strings.Contains(text, "working directory") {
		t.Errorf("the agent should be told the fallback was used, got:\n%s", text)
	}
}

// TestExplicitProjectDirBeatsRoots: an operator override must win, and it keeps
// the same .visual-companion layout.
func TestExplicitProjectDirBeatsRoots(t *testing.T) {
	rootsWorkspace := t.TempDir()
	pinned := t.TempDir()

	cs, _ := newPair(t, pinned, []string{rootsWorkspace})
	out, text := callToolText(t, cs, "start_companion", nil)

	dir, _ := out["session_dir"].(string)
	wantRoot := canonical(t, filepath.Join(pinned, sessionDirName))
	if !strings.HasPrefix(canonical(t, dir), wantRoot) {
		t.Errorf("session_dir = %q, want the pinned %q", dir, wantRoot)
	}
	if strings.Contains(dir, rootsWorkspace) {
		t.Errorf("roots must not override an explicit project dir, got %q", dir)
	}
	if !strings.Contains(text, "--project-dir") && !strings.Contains(text, "--session-dir") {
		t.Errorf("the agent should be told the pin came from a flag, got:\n%s", text)
	}
}

// TestUnusableWorkspaceIsRejected: launchers frequently start processes with
// cwd set to / or $HOME. Those must not become the place mockups are written.
func TestUnusableWorkspaceIsRejected(t *testing.T) {
	cs, _ := newPair(t, "", []string{string(filepath.Separator)})
	out, _ := callToolText(t, cs, "start_companion", nil)

	dir, _ := out["session_dir"].(string)
	if dir == "" {
		t.Fatal("no session_dir returned")
	}
	if filepath.Dir(filepath.Dir(filepath.Dir(dir))) == string(filepath.Separator) {
		t.Errorf("session landed at the filesystem root: %q", dir)
	}
}

// TestDirFromFileURI covers the parsing rules directly, including the Windows
// drive-letter form.
func TestDirFromFileURI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path expectations")
	}
	cases := []struct {
		uri     string
		want    string
		wantErr bool
	}{
		{uri: "file:///home/me/project", want: "/home/me/project"},
		{uri: "file:///home/me/my%20project", want: "/home/me/my project"},
		{uri: "file:///home/me/project/", want: "/home/me/project"},
		{uri: "https://example.com/x", wantErr: true},
		{uri: "file://", wantErr: true},
	}
	for _, tc := range cases {
		got, err := dirFromFileURI(tc.uri)
		if tc.wantErr {
			if err == nil {
				t.Errorf("dirFromFileURI(%q) = %q, want an error", tc.uri, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("dirFromFileURI(%q): %v", tc.uri, err)
			continue
		}
		if got != tc.want {
			t.Errorf("dirFromFileURI(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

// TestListSessionsReportsWhereMockupsWent lets an agent answer "where did my
// mockups go?" without guessing.
func TestListSessionsReportsWhereMockupsWent(t *testing.T) {
	workspace := t.TempDir()
	cs, _ := newPair(t, "", []string{workspace})

	// Nothing running yet.
	empty, emptyText := callToolText(t, cs, "list_sessions", nil)
	if empty["count"].(float64) != 0 {
		t.Fatalf("expected no sessions, got %+v", empty)
	}
	if !strings.Contains(emptyText, "start_companion") {
		t.Errorf("empty listing should say what to do next, got: %s", emptyText)
	}

	callTool(t, cs, "start_companion", nil)
	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>x</h2>", "label": "one"})

	out, text := callToolText(t, cs, "list_sessions", nil)
	if out["count"].(float64) != 1 {
		t.Fatalf("expected 1 session, got %+v", out)
	}
	sessions, _ := out["sessions"].([]any)
	first, _ := sessions[0].(map[string]any)
	if dir, _ := first["session_dir"].(string); !strings.HasPrefix(dir, workspace) {
		t.Errorf("session_dir = %q, want it under %q", dir, workspace)
	}
	if from, _ := first["location_from"].(string); from != string(fromRoots) {
		t.Errorf("location_from = %q, want %q", from, fromRoots)
	}
	if !strings.Contains(text, workspace) {
		t.Errorf("listing should name the directory, got:\n%s", text)
	}
}

// TestListSessionsDoesNotConsumeEvents: listing is a read-only inspection, so
// it must not steal the user's clicks from get_events.
func TestListSessionsDoesNotConsumeEvents(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>x</h2>", "label": "one"})
	postClick(t, url, "a", "Choice A")

	listed, _ := callToolText(t, cs, "list_sessions", nil)
	sessions, _ := listed["sessions"].([]any)
	first, _ := sessions[0].(map[string]any)
	if first["pending_events"].(float64) != 1 {
		t.Errorf("listing should report 1 pending event, got %+v", first)
	}

	events := callTool(t, cs, "get_events", nil)
	if events["count"].(float64) != 1 {
		t.Errorf("list_sessions consumed the click; get_events saw %+v", events)
	}
}

// TestSessionsFromDifferentWorkspacesStaySeparate: one server can serve two
// workspaces if the client adds a root later, and each keeps its own files.
func TestSessionsFromDifferentWorkspacesStaySeparate(t *testing.T) {
	first := t.TempDir()
	cs, reg := newPair(t, "", []string{first})
	callTool(t, cs, "start_companion", nil)

	sessions := reg.ActiveSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if !strings.HasPrefix(sessions[0].Dir, first) {
		t.Errorf("session landed at %q, want it under %q", sessions[0].Dir, first)
	}
}

// --- helpers ---------------------------------------------------------------

// canonical resolves symlinks so a path can be compared with one the server
// derived. macOS is why this exists: /var is a symlink to /private/var, so
// t.TempDir() reports /var/... while os.Getwd() after a chdir reports
// /private/var/..., and a naive prefix check fails on a correct implementation.
func canonical(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// The leaf may not exist yet; canonicalize the part that does.
		if parent, perr := filepath.EvalSymlinks(filepath.Dir(path)); perr == nil {
			return filepath.Join(parent, filepath.Base(path))
		}
		return filepath.Clean(path)
	}
	return resolved
}

func chdir(t *testing.T, dir string) func() {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return func() { _ = os.Chdir(old) }
}
