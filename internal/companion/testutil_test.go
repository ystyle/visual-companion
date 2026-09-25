package companion

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var (
	assetsOnce sync.Once
	assetsVal  Assets
	assetsErr  error
)

// testAssets loads the real embedded browser assets rather than fixtures, so
// the tests exercise the files that actually ship.
func testAssets() Assets {
	assetsOnce.Do(func() {
		root := filepath.Join("..", "..", "assets")
		frame, err := os.ReadFile(filepath.Join(root, "frame-template.html"))
		if err != nil {
			assetsErr = err
			return
		}
		helper, err := os.ReadFile(filepath.Join(root, "helper.js"))
		if err != nil {
			assetsErr = err
			return
		}
		assetsVal = Assets{Frame: frame, Helper: helper}
	})
	if assetsErr != nil {
		panic("cannot load assets for tests: " + assetsErr.Error())
	}
	return assetsVal
}

// startTestSession brings up a real HTTP session on a loopback port.
func startTestSession(t *testing.T) *Session {
	t.Helper()
	s, err := NewSession(t.TempDir())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.SetAssets(testAssets())
	if _, err := s.Start("127.0.0.1", "", false); err != nil {
		t.Fatalf("start session: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
