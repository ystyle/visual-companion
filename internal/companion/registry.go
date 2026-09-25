package companion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Registry owns the companion sessions started during one MCP server's life.
//
// Sessions live only as long as the MCP host keeps this process alive, which
// is exactly the lifetime the browser tab needs: no pid files, no detached
// daemons, no idle-timeout watchdog.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
	order    []string

	assets      Assets
	sessionDir  string // explicit --session-dir, wins over everything
	useRoots    bool   // consult the client's MCP roots for a workspace
	host        string
	urlHost     string
	openBrowser bool
}

// NewRegistry builds a registry. sessionDir may be empty, in which case the
// location is resolved per session from the client's roots or the working
// directory.
func NewRegistry(assets Assets, sessionDir, host, urlHost string, openBrowser bool) *Registry {
	return &Registry{
		sessions:    map[string]*Session{},
		assets:      assets,
		sessionDir:  sessionDir,
		useRoots:    true,
		host:        host,
		urlHost:     urlHost,
		openBrowser: openBrowser,
	}
}

// SetRootsDiscovery enables or disables consulting the client's MCP roots.
func (r *Registry) SetRootsDiscovery(enabled bool) { r.useRoots = enabled }

// Start creates a session without a client, resolving the location from the
// registry's own configuration.
func (r *Registry) Start() (*SessionInfo, error) {
	return r.StartIn(context.Background(), nil, r.sessionDir)
}

// StartWithClient starts a session for a real tool call, resolving the location
// from the operator's pin, then the client's MCP roots, then the working
// directory.
//
// The location is resolved here, at start_companion time, rather than at
// process launch: service-shaped agents boot the MCP server before a workspace
// exists, so the process's initial directory is meaningless.
func (r *Registry) StartWithClient(ctx context.Context, sess *mcp.ServerSession) (*SessionInfo, error) {
	return r.StartIn(ctx, sess, r.sessionDir)
}

// StartIn resolves where a session should live and brings it up. An empty
// projectDir falls back to the registry's pinned directory.
func (r *Registry) StartIn(ctx context.Context, sess *mcp.ServerSession, projectDir string) (*SessionInfo, error) {
	if projectDir == "" {
		projectDir = r.sessionDir
	}
	loc := resolveLocation(ctx, sess, projectDir, r.useRoots)

	s, err := NewSession(sessionRootUnder(loc))
	if err != nil {
		return nil, err
	}
	s.SetAssets(r.assets)
	s.location = loc

	info, err := s.Start(r.host, r.urlHost, r.openBrowser)
	if err != nil {
		_ = s.Close()
		return nil, err
	}

	r.mu.Lock()
	r.sessions[s.ID] = s
	r.order = append(r.order, s.ID)
	r.mu.Unlock()
	return info, nil
}

// Resolve finds the session a tool call refers to. An empty id means "the most
// recently started session", which is what an agent wants in the common case
// of one companion per conversation.
func (r *Registry) Resolve(sessionID string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.sessions) == 0 {
		return nil, fmt.Errorf("no companion session is running; call start_companion first")
	}
	if sessionID == "" {
		return r.sessions[r.order[len(r.order)-1]], nil
	}
	s, ok := r.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("unknown session_id %q; call start_companion to see active sessions", sessionID)
	}
	return s, nil
}

// CloseAll shuts every session down. Called on process exit.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	sessions := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.sessions = map[string]*Session{}
	r.order = nil
	r.mu.Unlock()

	for _, s := range sessions {
		_ = s.Close()
	}
}

// ActiveIDs lists live sessions, newest last.
func (r *Registry) ActiveIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// ActiveSessions returns live sessions, newest last.
func (r *Registry) ActiveSessions() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Session, 0, len(r.order))
	for _, id := range r.order {
		if s, ok := r.sessions[id]; ok {
			out = append(out, s)
		}
	}
	return out
}

// SessionDirOverride reports the --session-dir pin, if any.
func (r *Registry) SessionDirOverride() string { return r.sessionDir }

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a screen name into a safe filename stem.
func slugify(label string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(label), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "screen"
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// --- designs and versions --------------------------------------------------
//
// Every screen lives at <content>/<design>/v<N>/screen.html. The version number
// is the human-facing handle for "which round of this design is on screen":
// it shows in the browser badge, it is in the path, and the agent gets it back
// from push_screen. Mockups stay diffable and reviewable after the session ends
// because nothing is ever overwritten.

// Designs lists the design names in this session, sorted.
func (s *Session) Designs() []string {
	entries, err := os.ReadDir(s.ContentDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == assetsDirName {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// latestVersion returns the highest version directory for a design, or 0.
func (s *Session) latestVersion(design string) int {
	entries, err := os.ReadDir(filepath.Join(s.ContentDir(), design))
	if err != nil {
		return 0
	}
	highest := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n, ok := parseVersionDir(e.Name()); ok && n > highest {
			highest = n
		}
	}
	return highest
}

// nextScreen allocates the next version of a design and creates its directory.
//
// Versions come from the highest existing directory, not a counter, so a
// deleted version is never reused and an out-of-order write cannot collide.
func (s *Session) nextScreen(design string) (screenEntry, error) {
	slug := slugify(design)

	s.versionMu.Lock()
	defer s.versionMu.Unlock()

	// Re-scan under the lock: two concurrent pushes of the same design must not
	// both land on v1.
	version := s.latestVersion(slug) + 1
	for {
		dir := filepath.Join(s.ContentDir(), slug, versionDirName(version))
		err := os.MkdirAll(dir, 0o755)
		if err == nil {
			return screenEntry{
				design:  slug,
				version: version,
				path:    filepath.Join(dir, screenFileName),
			}, nil
		}
		if !os.IsExist(err) {
			return screenEntry{}, fmt.Errorf("create %s: %w", dir, err)
		}
		version++ // someone else got there first
	}
}

// versionDirName renders the directory name for a version.
func versionDirName(n int) string { return fmt.Sprintf("v%d", n) }

// parseVersionDir reads a version directory name. Only the exact v<digits> form
// counts, so a stray directory cannot shift the numbering.
func parseVersionDir(name string) (int, bool) {
	if len(name) < 2 || name[0] != 'v' {
		return 0, false
	}
	digits := name[1:]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// assetsPath is where shared assets for a session live. A screen can reference
// them as /files/<name>.
func (s *Session) assetsPath() string { return filepath.Join(s.ContentDir(), assetsDirName) }

// waitForGeneration blocks until the screen watcher has announced a change
// past `from`.
//
// Checking the newest file instead does not work: currentScreen() rescans the
// directory on demand, so it reports the freshly written file immediately,
// before the watcher has bumped the generation or cleared stale events. The
// generation is the only honest signal that the new round has begun.
func (s *Session) waitForGeneration(from int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.Generation() > from {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// sortedEvents orders interactions the way they happened.
func sortedEvents(events []Event) []Event {
	sort.SliceStable(events, func(i, j int) bool { return events[i].Timestamp < events[j].Timestamp })
	return events
}
