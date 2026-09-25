package companion

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Where a session's files live.
//
// This is resolved lazily, per start_companion call, rather than fixed at
// process launch. Service-shaped agents (dsh, opencode, and anything else that
// boots the MCP server before a workspace exists) start the process in a
// directory that has nothing to do with the conversation, so a path captured at
// startup would be wrong.
type locationSource string

const (
	// fromFlag: the operator passed --session-dir. Always wins.
	fromFlag locationSource = "--session-dir"
	// fromRoots: the client told us its workspace over MCP roots. The correct
	// answer for a service-shaped agent.
	fromRoots locationSource = "client roots"
	// fromCwd: no roots support, but the process's directory looks like a
	// usable workspace.
	fromCwd locationSource = "working directory"
	// fromTemp: nothing else was usable. Sessions still work; the mockups just
	// do not outlive the process.
	fromTemp locationSource = "temporary directory"
)

type location struct {
	base   string
	source locationSource
}

// describe explains the choice to the agent and the user.
func (l location) describe() string {
	switch l.source {
	case fromFlag:
		return fmt.Sprintf("Sessions are stored in %s (from --session-dir).", l.base)
	case fromRoots:
		return fmt.Sprintf("Sessions are stored in %s (your workspace, reported by the client).", l.base)
	case fromCwd:
		return fmt.Sprintf("Sessions are stored in %s (the server's working directory; "+
			"this client does not report roots, so pass --session-dir to control it).", l.base)
	default:
		return fmt.Sprintf("Sessions are stored in %s. This client reports no workspace and the "+
			"working directory is not usable, so mockups will not persist after the session ends. "+
			"Pass --session-dir to keep them.", l.base)
	}
}

// resolveLocation decides where this session's files go.
//
// Precedence: --session-dir > client roots > working directory > temp. Roots
// are queried at this moment, not cached from startup, because that is when the
// workspace is known to be real.
func resolveLocation(ctx context.Context, sess *mcp.ServerSession, flagDir string, useRoots bool) location {
	if flagDir != "" {
		if abs, err := filepath.Abs(flagDir); err == nil {
			flagDir = abs
		}
		return location{base: flagDir, source: fromFlag}
	}

	if useRoots && sess != nil {
		if dir, ok := workspaceFromRoots(ctx, sess); ok {
			return location{base: dir, source: fromRoots}
		}
	}

	if wd, err := os.Getwd(); err == nil && usableWorkspace(wd) {
		return location{base: wd, source: fromCwd}
	}

	return location{base: os.TempDir(), source: fromTemp}
}

// workspaceFromRoots asks the client for its roots and returns the first usable
// workspace directory.
//
// There is no reliable capability pre-check: ClientCapabilities.Roots is a
// value type that serializes as "roots":{} whether or not the client supports
// roots, and the newer RootsV2 pointer is marked json:"-" so it never reaches
// the server. So we simply ask, with a timeout, and treat every failure as
// "this client has no workspace to tell us about" — which is the same path a
// non-roots client takes. The SDK's own internal root handling does the same.
func workspaceFromRoots(ctx context.Context, sess *mcp.ServerSession) (string, bool) {
	// Bound the wait: a client that does not implement roots may never answer,
	// and start_companion must not stall behind it.
	ctx, cancel := context.WithTimeout(ctx, rootsTimeout)
	defer cancel()

	res, err := sess.ListRoots(ctx, nil)
	if err != nil || res == nil {
		return "", false
	}
	for _, r := range res.Roots {
		if r == nil {
			continue
		}
		dir, err := dirFromFileURI(r.URI)
		if err != nil {
			continue
		}
		if usableWorkspace(dir) {
			return dir, true
		}
	}
	return "", false
}

// dirFromFileURI converts a root URI to a local directory path. Roots must be
// file:// URIs; anything else is ignored rather than guessed at.
func dirFromFileURI(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("root %q is not a file:// URI", uri)
	}
	path := parsed.Path
	// Windows roots look like file:///C:/Users/... and need the leading slash
	// removed before the path is usable.
	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	if platformPath, err := url.PathUnescape(path); err == nil {
		path = platformPath
	}
	if path == "" {
		return "", fmt.Errorf("root %q has no path", uri)
	}
	return filepath.Clean(path), nil
}

// usableWorkspace rejects directories that would surprise the user as a place
// to write mockups: the filesystem root, a home directory itself, and the
// system temp dirs. Those are exactly what a launcher's working directory
// tends to be.
func usableWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)

	if abs == string(filepath.Separator) || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return false // filesystem root
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if abs == filepath.Clean(home) {
			return false
		}
	}
	for _, tmp := range tempDirs() {
		if abs == filepath.Clean(tmp) {
			return false
		}
	}
	// A directory that does not exist cannot hold a session.
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return false
	}
	return true
}

func tempDirs() []string {
	dirs := []string{os.TempDir()}
	if runtime.GOOS != "windows" {
		dirs = append(dirs, "/tmp", "/var/tmp")
	}
	return dirs
}

// sessionRootUnder returns the directory that holds this session's designs.
//
// The chosen location is the user's *project* directory; mockups always live
// under a `.visual-companion/` subdirectory of it. That keeps one rule for
// every source (flag, roots, cwd, temp) instead of a special case, and it means
// a user who points --project-dir at their repo gets the same layout an
// auto-discovered workspace produces.
func sessionRootUnder(loc location) string {
	return filepath.Join(loc.base, sessionDirName)
}

// pathLooksLikeWorkspace is a cheap sanity check used when describing a
// location, so the agent can tell a real project from a bare directory.
func pathLooksLikeWorkspace(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		switch strings.ToLower(e.Name()) {
		case ".git", "go.mod", "package.json", "cargo.toml", "pyproject.toml", "pom.xml":
			return true
		}
	}
	return false
}
