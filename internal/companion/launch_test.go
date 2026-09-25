package companion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBrowserForPlatform(t *testing.T) {
	cases := []struct {
		name       string
		goos       string
		hasDisplay bool
		isWSL      bool
		wantBin    string
		wantNil    bool
	}{
		{name: "macOS always has a launcher", goos: "darwin", wantBin: "open"},
		{name: "windows uses the URL handler", goos: "windows", wantBin: "rundll32.exe"},
		{name: "linux with a display", goos: "linux", hasDisplay: true, wantBin: "xdg-open"},
		{name: "headless linux has none", goos: "linux", wantNil: true},
		{name: "WSL uses the windows handler", goos: "linux", isWSL: true, wantBin: "rundll32.exe"},
		{name: "unknown platform has none", goos: "plan9", wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserForPlatform(tc.goos, tc.hasDisplay, tc.isWSL)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected no launcher, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a launcher, got nil")
			}
			if got.Bin != tc.wantBin {
				t.Errorf("Bin = %q, want %q", got.Bin, tc.wantBin)
			}
		})
	}
}

func TestLaunchPassesURLAsAnArgument(t *testing.T) {
	// A url-host containing shell metacharacters must arrive as one argv
	// element, never interpolated into a shell string.
	nasty := "http://localhost:1/?key=a;rm -rf /"
	var gotBin string
	var gotArgs []string

	err := LaunchBrowserFor(nasty, "darwin", false, false, func(bin string, args ...string) error {
		gotBin, gotArgs = bin, args
		return nil
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if gotBin != "open" {
		t.Errorf("bin = %q", gotBin)
	}
	if len(gotArgs) != 1 || gotArgs[0] != nasty {
		t.Errorf("url must be a single untouched argv element, got %q", gotArgs)
	}
}

func TestLaunchOnHeadlessReturnsError(t *testing.T) {
	called := false
	err := LaunchBrowserFor("http://x/", "linux", false, false, func(string, ...string) error {
		called = true
		return nil
	})
	if err == nil {
		t.Error("headless launch should report that there is nothing to open")
	}
	if called {
		t.Error("headless launch must not execute anything")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"layout":            "layout",
		"Visual Style":      "visual-style",
		"visual_style":      "visual-style",
		"  spaced  out  ":   "spaced-out",
		"":                  "screen",
		"!!!":               "screen",
		"Layout v2 (final)": "layout-v2-final",
		"UPPER":             "upper",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := slugify(strings.Repeat("a", 100)); len(got) > 48 {
		t.Errorf("slug should be truncated, got %d chars", len(got))
	}
}

func TestVersionsIncrementAndNeverReuseNumbers(t *testing.T) {
	s, err := NewSession(t.TempDir())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer s.Close()

	var paths []string
	for i := 1; i <= 3; i++ {
		entry, err := s.nextScreen("Dashboard Layout")
		if err != nil {
			t.Fatalf("nextScreen: %v", err)
		}
		if entry.design != "dashboard-layout" {
			t.Errorf("design = %q, want it slugified", entry.design)
		}
		if entry.version != i {
			t.Errorf("version = %d, want %d", entry.version, i)
		}
		// The version must be visible in the path: that is what makes mockups
		// reviewable after the session ends.
		want := filepath.Join(s.ContentDir(), "dashboard-layout", fmt.Sprintf("v%d", i), "screen.html")
		if entry.path != want {
			t.Errorf("path = %q, want %q", entry.path, want)
		}
		if err := os.WriteFile(entry.path, []byte("<h2>x</h2>"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		paths = append(paths, entry.path)
	}

	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("screen path reused: %s", p)
		}
		seen[p] = true
	}
}

// Version numbers are "highest existing directory + 1", which keeps numbering
// monotonic through the normal workflow and never collides. A gap left by a
// human deleting a version directory is stepped over, not filled.
func TestVersionsFollowHighestExistingDirectory(t *testing.T) {
	s, _ := NewSession(t.TempDir())
	defer s.Close()

	first, _ := s.nextScreen("layout")
	_ = os.WriteFile(first.path, []byte("x"), 0o644)
	second, _ := s.nextScreen("layout")
	_ = os.WriteFile(second.path, []byte("x"), 0o644)
	if second.version != 2 {
		t.Fatalf("version = %d, want 2", second.version)
	}

	// Delete the newest version: the next allocation must not hand it out
	// again while older versions are still on disk.
	if err := os.RemoveAll(filepath.Dir(second.path)); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// v1 still exists, so the highest is 1 and the next is 2. That number is
	// only "reused" because a human removed the directory; nothing the tool
	// does reuses one.
	third, _ := s.nextScreen("layout")
	if third.version != 2 {
		t.Errorf("version = %d, want 2 (highest existing + 1)", third.version)
	}

	// With a gap (v1 and v3 present), the next version is the highest + 1.
	fourth, _ := s.nextScreen("layout")
	_ = os.WriteFile(fourth.path, []byte("x"), 0o644)
	fifth, _ := s.nextScreen("layout")
	if fifth.version != 4 {
		t.Errorf("version = %d, want 4 after v1..v3", fifth.version)
	}
}

func TestConcurrentPushesGetDistinctVersions(t *testing.T) {
	s, _ := NewSession(t.TempDir())
	defer s.Close()

	const n = 8
	versions := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, err := s.nextScreen("layout")
			if err != nil {
				t.Errorf("nextScreen: %v", err)
				return
			}
			versions <- entry.version
		}()
	}
	wg.Wait()
	close(versions)

	seen := map[int]bool{}
	for v := range versions {
		if seen[v] {
			t.Errorf("version %d allocated twice", v)
		}
		seen[v] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct versions, want %d", len(seen), n)
	}
}

func TestParseVersionDir(t *testing.T) {
	cases := map[string]struct {
		n  int
		ok bool
	}{
		"v1":     {1, true},
		"v12":    {12, true},
		"v0":     {0, false},
		"v":      {0, false},
		"v1a":    {0, false},
		"1":      {0, false},
		"V1":     {0, false},
		"v-1":    {0, false},
		"notes":  {0, false},
		"v1.bak": {0, false},
	}
	for in, want := range cases {
		got, ok := parseVersionDir(in)
		if ok != want.ok || (ok && got != want.n) {
			t.Errorf("parseVersionDir(%q) = (%d, %v), want (%d, %v)", in, got, ok, want.n, want.ok)
		}
	}
}

func TestDesignsAreListed(t *testing.T) {
	s, _ := NewSession(t.TempDir())
	defer s.Close()

	if got := s.Designs(); len(got) != 0 {
		t.Fatalf("fresh session has designs: %v", got)
	}
	for _, name := range []string{"checkout", "homepage"} {
		entry, _ := s.nextScreen(name)
		_ = os.WriteFile(entry.path, []byte("x"), 0o644)
	}
	got := s.Designs()
	want := []string{"checkout", "homepage"}
	if len(got) != len(want) {
		t.Fatalf("Designs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Designs()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestHelperPureFunctions runs the shipped helper.js under node to check the
// pure watchdog predicates. Skipped when node is unavailable.
func TestHelperPureFunctions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping JS unit checks")
	}
	helperPath, err := filepath.Abs(filepath.Join("..", "..", "assets", "helper.js"))
	if err != nil {
		t.Fatalf("resolve helper path: %v", err)
	}

	// Run a real file rather than -e: argv offsets differ between the two, and
	// we want the module path to be the first argument. The path is passed as
	// an absolute filesystem path (require() rejects a file:// URL here).
	script := filepath.Join(t.TempDir(), "check.js")
	body := `
const h = require(process.argv[2]);
console.log(JSON.stringify({
  fresh:      h.isStale(0, 100000, h.STALE_AFTER_MS),
  justNow:    h.isStale(100000, 100100, h.STALE_AFTER_MS),
  longAgo:    h.isStale(100000, 100000 + h.STALE_AFTER_MS + 1, h.STALE_AFTER_MS),
  noOverlay:  h.shouldShowOverlay(null, 100000, h.OFFLINE_AFTER_MS),
  notYet:     h.shouldShowOverlay(100000, 100000 + h.OFFLINE_AFTER_MS - 1, h.OFFLINE_AFTER_MS),
  overlayNow: h.shouldShowOverlay(100000, 100000 + h.OFFLINE_AFTER_MS, h.OFFLINE_AFTER_MS),
}));
`
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatalf("write check script: %v", err)
	}

	raw, err := exec.Command(node, script, helperPath).CombinedOutput()
	if err != nil {
		t.Fatalf("node run failed: %v\n%s", err, raw)
	}
	got := strings.TrimSpace(string(raw))
	if !strings.Contains(got, "longAgo") {
		t.Fatalf("helper.js exports are not reachable — if this says {}, the assets "+
			"directory is being treated as ESM; check package.json type: %s", got)
	}
	for _, want := range []string{
		`"fresh":false`, `"justNow":false`, `"longAgo":true`,
		`"noOverlay":false`, `"notYet":false`, `"overlayNow":true`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("helper.js predicate check missing %s in %s", want, got)
		}
	}
}
