package companion

import (
	"os"
	"testing"
)

// TestMain removes any workspace that a test's working-directory fallback may
// have created inside the package. resolveLocation deliberately falls back to
// the process's directory, so a test that does not pin a session dir will write
// one here.
func TestMain(m *testing.M) {
	code := m.Run()
	_ = os.RemoveAll(sessionDirName)
	os.Exit(code)
}
