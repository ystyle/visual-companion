package companion

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// browserCommand describes how to hand a URL to the platform's default
// browser. Kept separate from execution so it can be unit tested on any host.
type browserCommand struct {
	Bin  string
	Args []string
}

// browserForPlatform returns the launcher for a given GOOS, or nil when the
// platform has no browser to open (headless Linux, for example).
//
// The original shell implementation had to detect WSL and MSYS separately. Go
// gives us GOOS, and WSL is the one case still worth special-casing.
func browserForPlatform(goos string, hasDisplay bool, isWSL bool) *browserCommand {
	switch goos {
	case "darwin":
		return &browserCommand{Bin: "open"}
	case "windows":
		return &browserCommand{Bin: "rundll32.exe", Args: []string{"url.dll,FileProtocolHandler"}}
	case "linux":
		if isWSL {
			// WSL has no X socket for xdg-open; Windows' handler works.
			return &browserCommand{Bin: "rundll32.exe", Args: []string{"url.dll,FileProtocolHandler"}}
		}
		if !hasDisplay {
			return nil
		}
		return &browserCommand{Bin: "xdg-open"}
	default:
		return nil
	}
}

// LaunchBrowserFor is the testable core of browser launching.
func LaunchBrowserFor(url, goos string, hasDisplay, isWSL bool, run func(bin string, args ...string) error) error {
	cmd := browserForPlatform(goos, hasDisplay, isWSL)
	if cmd == nil {
		return os.ErrNotExist
	}
	return run(cmd.Bin, append(cmd.Args, url)...)
}

// launchBrowser opens the user's browser. Best effort: on a headless or
// remote box there is nothing to open, and the agent still has the URL.
func launchBrowser(url string) error {
	return LaunchBrowserFor(url, runtime.GOOS, hasDisplay(), isWSL(), func(bin string, args ...string) error {
		return exec.Command(bin, args...).Start()
	})
}

func hasDisplay() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// isWSL detects Windows Subsystem for Linux via the kernel release string.
func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}
