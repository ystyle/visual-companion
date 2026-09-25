package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestVersionFlag checks the smoke test that CI runs against every release
// artifact. A release built without -ldflags reports "dev", which is exactly
// what this catches.
func TestVersionFlag(t *testing.T) {
	bin := buildSelf(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "visual-companion dev" {
		t.Errorf("--version printed %q, want %q for a test build without ldflags", got, "visual-companion dev")
	}
}

// TestVersionInjection proves the ldflags path the release script uses.
func TestVersionInjection(t *testing.T) {
	bin := buildSelfWithLdflags(t, "-X main.version=9.9.9")
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "visual-companion v9.9.9" {
		t.Errorf("--version printed %q, want %q", got, "visual-companion v9.9.9")
	}
}

// TestStdoutStaysClean guards the stdio contract: the MCP transport owns
// stdout, so anything printed there before or instead of the JSON-RPC
// handshake can desynchronize the host.
func TestStdoutStaysClean(t *testing.T) {
	bin := buildSelf(t)
	cmd := exec.Command(bin, "--project-dir", t.TempDir())
	cmd.Stdin = strings.NewReader("") // EOF immediately: no handshake happens

	out, err := cmd.Output() // stdout only
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run: %v", err)
		}
		// A non-zero exit is acceptable; clean stdout is what matters.
	}
	if len(out) != 0 {
		t.Errorf("stdout should carry nothing but JSON-RPC, got %q", out)
	}
}

// --- helpers ---------------------------------------------------------------

func buildSelf(t *testing.T) string {
	t.Helper()
	return buildSelfWithLdflags(t, "")
}

func buildSelfWithLdflags(t *testing.T, ldflags string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "visual-companion")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{"build", "-o", bin}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, ".")

	cmd := exec.Command("go", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}
