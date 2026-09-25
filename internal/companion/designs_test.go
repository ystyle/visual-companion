package companion

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBadgeShowsDesignAndVersion is the point of the whole versioning scheme:
// the human must be able to tell at a glance which round they are looking at.
func TestBadgeShowsDesignAndVersion(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)

	callTool(t, cs, "push_screen", map[string]any{
		"html":   "<h2>Round one</h2>",
		"design": "Dashboard Layout",
	})

	body := fetchScreen(t, url)
	if !strings.Contains(body, "dashboard-layout") {
		t.Errorf("badge should name the slugified design, got: %.300s", body)
	}
	if !strings.Contains(body, `class="badge-version">v1<`) {
		t.Errorf("badge should show v1, got: %.300s", body)
	}
	if strings.Contains(body, "<!-- BADGE -->") {
		t.Error("badge placeholder was not substituted")
	}

	// Revising the same design becomes v2, and the page says so.
	callTool(t, cs, "push_screen", map[string]any{
		"html":   "<h2>Round two</h2>",
		"design": "Dashboard Layout",
	})
	body = fetchScreen(t, url)
	if !strings.Contains(body, `class="badge-version">v2<`) {
		t.Errorf("second version should render as v2, got: %.300s", body)
	}
	if strings.Contains(body, "Round one") {
		t.Error("the newest version should be the one served")
	}
}

// TestVersionedLayoutOnDisk is what makes mockups reviewable after the session:
// nothing is overwritten, and the version is in the path.
func TestVersionedLayoutOnDisk(t *testing.T) {
	cs, reg := newTestPair(t)
	callTool(t, cs, "start_companion", nil)

	for i := 1; i <= 3; i++ {
		out := callTool(t, cs, "push_screen", map[string]any{
			"html":   fmt.Sprintf("<h2>Rev %d</h2>", i),
			"design": "homepage",
		})
		if out["design"] != "homepage" {
			t.Errorf("design = %v, want homepage", out["design"])
		}
		if out["version"].(float64) != float64(i) {
			t.Errorf("version = %v, want %d", out["version"], i)
		}
	}

	s, err := reg.Resolve("")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for i := 1; i <= 3; i++ {
		p := filepath.Join(s.ContentDir(), "homepage", fmt.Sprintf("v%d", i), "screen.html")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("v%d not on disk at %s: %v", i, p, err)
		}
		if !strings.Contains(string(data), fmt.Sprintf("Rev %d", i)) {
			t.Errorf("v%d holds the wrong content: %s", i, data)
		}
	}
}

// TestDesignsAreIndependent: two designs coexist, each with its own numbering.
func TestDesignsAreIndependent(t *testing.T) {
	cs, reg := newTestPair(t)
	callTool(t, cs, "start_companion", nil)

	a1 := callTool(t, cs, "push_screen", map[string]any{"html": "<h2>A1</h2>", "design": "alpha"})
	_ = callTool(t, cs, "push_screen", map[string]any{"html": "<h2>B1</h2>", "design": "beta"})
	a2 := callTool(t, cs, "push_screen", map[string]any{"html": "<h2>A2</h2>", "design": "alpha"})

	if a1["version"].(float64) != 1 || a2["version"].(float64) != 2 {
		t.Errorf("alpha versions = %v then %v, want 1 then 2", a1["version"], a2["version"])
	}

	s, _ := reg.Resolve("")
	if got := s.Designs(); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("Designs() = %v, want [alpha beta]", got)
	}
}

// TestMissingDesignNameStillWorks: the parameter is encouraged but omitting it
// must not fail a push.
func TestMissingDesignNameStillWorks(t *testing.T) {
	cs, reg := newTestPair(t)
	callTool(t, cs, "start_companion", nil)

	out := callTool(t, cs, "push_screen", map[string]any{"html": "<h2>No name</h2>"})
	if out["status"] != "pushed" {
		t.Fatalf("push without a design name failed: %+v", out)
	}
	s, _ := reg.Resolve("")
	if got := s.Designs(); len(got) != 1 || got[0] != "design" {
		t.Errorf("Designs() = %v, want the [design] fallback", got)
	}
}

// TestFullDocumentGetsFloatingBadge: a full document has no frame header, so the
// badge is floated instead of injected into markup we do not own.
func TestFullDocumentGetsFloatingBadge(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)

	callTool(t, cs, "push_screen", map[string]any{
		"html":   "<!DOCTYPE html><html><body><h1>Custom</h1></body></html>",
		"design": "custom-page",
	})

	body := fetchScreen(t, url)
	if !strings.Contains(body, "<h1>Custom</h1>") {
		t.Fatal("full document content missing")
	}
	if strings.Contains(body, "id=\"frame-content\"") {
		t.Error("a full document must not be wrapped in the frame")
	}
	if !strings.Contains(body, "custom-page") || !strings.Contains(body, "v1") {
		t.Errorf("full document should still carry a version badge, got: %.300s", body)
	}
}

// TestListSessionsReportsDesigns lets the agent answer "what have I drawn and
// how far did I get?" without filesystem access.
func TestListSessionsReportsDesigns(t *testing.T) {
	cs, _ := newTestPair(t)
	callTool(t, cs, "start_companion", nil)
	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>1</h2>", "design": "hero"})
	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>2</h2>", "design": "hero"})
	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>3</h2>", "design": "footer"})

	out, text := callToolText(t, cs, "list_sessions", nil)
	sessions, _ := out["sessions"].([]any)
	first, _ := sessions[0].(map[string]any)
	designs, _ := first["designs"].([]any)
	if len(designs) != 2 {
		t.Fatalf("expected 2 designs, got %+v", designs)
	}

	byName := map[string]float64{}
	for _, d := range designs {
		m, _ := d.(map[string]any)
		name, _ := m["design"].(string)
		v, _ := m["latest_version"].(float64)
		byName[name] = v
	}
	if byName["hero"] != 2 {
		t.Errorf("hero latest version = %v, want 2", byName["hero"])
	}
	if byName["footer"] != 1 {
		t.Errorf("footer latest version = %v, want 1", byName["footer"])
	}
	for _, want := range []string{"hero", "footer", "v2", "v1"} {
		if !strings.Contains(text, want) {
			t.Errorf("listing should mention %q, got:\n%s", want, text)
		}
	}
}
