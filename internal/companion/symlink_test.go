package companion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceThroughASymlinkedPath is the regression test for the macOS CI
// failure: there, /var is a symlink to /private/var, so a test's t.TempDir()
// reports one path while the server (via os.Getwd after a chdir) reports the
// other. Both name the same directory and the session must land in it.
//
// Linux has no such /var link, so the test creates its own symlinked path
// rather than relying on the platform to provide one.
func TestWorkspaceThroughASymlinkedPath(t *testing.T) {
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "linked-workspace")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}

	// Resolve every derived path before comparing, exactly as the session does
	// not: the point is that canonical() makes the two spellings agree.
	if !strings.HasPrefix(canonical(t, filepath.Join(link, sessionDirName)),
		canonical(t, filepath.Join(real, sessionDirName))) {
		t.Fatal("canonical() failed to reconcile a symlinked workspace with its target")
	}

	// And the server actually writes through the link to the real directory.
	restore := chdir(t, link)
	defer restore()

	cs, _ := newPair(t, "", nil)
	out, _ := callToolText(t, cs, "start_companion", nil)
	dir, _ := out["session_dir"].(string)

	if !strings.HasPrefix(canonical(t, dir), canonical(t, filepath.Join(real, sessionDirName))) {
		t.Errorf("session_dir = %q, want it under the real path %q (canonical %q)",
			dir, filepath.Join(real, sessionDirName), canonical(t, dir))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("session directory is not reachable: %v", err)
	}
}
