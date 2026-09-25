// Package companion implements the browser-facing half of the visual
// companion: an HTTP server that serves whatever HTML screen the agent last
// pushed, and records the user's browser interactions to disk.
//
// The agent never sees the page. It pushes a screen and reads back clicks.
package companion

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// sessionDirName is the workspace-relative directory every mockup lives
	// under: <workspace>/.visual-companion/<design>/v<N>/screen.html
	sessionDirName = ".visual-companion"

	// contentDirName holds the design directories inside one session.
	contentDirName = "content"
	// stateDirName holds events, server metadata and the session key.
	stateDirName = "state"
	// assetsDirName holds shared assets a screen can reference as
	// /files/<name> without living in a version directory.
	assetsDirName = "assets"

	// screenFileName is the screen inside each version directory.
	screenFileName = "screen.html"

	// watchInterval is how often we poll for new screens. The original Node
	// implementation used fs.watch; polling is simpler, portable, and 250ms
	// of latency is invisible next to a human reading a mockup.
	watchInterval = 250 * time.Millisecond

	// heartbeatInterval keeps SSE connections alive through intermediaries
	// that close idle sockets, and lets the browser detect a dead server.
	heartbeatInterval = 15 * time.Second

	// rootsTimeout bounds the MCP roots query. A client that advertises roots
	// but answers slowly must not stall start_companion.
	rootsTimeout = 5 * time.Second
)

// Session is one companion session: a directory pair plus the HTTP server
// that exposes it to the browser.
type Session struct {
	ID         string // stable, human-readable session id
	Dir        string // <session>/ containing content/ and state/
	ProjectDir string

	httpSrv  *http.Server
	listener net.Listener
	port     int
	urlHost  string
	key      string

	bus *bus

	// assets are the embedded browser files; set once via Configure.
	assets Assets

	// location records where this session's files went and why, so the agent
	// can tell the user something accurate about persistence.
	location location

	mu         sync.Mutex
	knownFiles map[string]bool      // relative screen paths seen at the last poll
	seenTimes  map[string]time.Time // their mtimes, to tell an update from a no-op
	newestFile string
	current    screenEntry // design/version identity of newestFile
	generation int
	eventsPath string
	infoPath   string

	// versionMu serializes version allocation so two concurrent pushes of the
	// same design cannot both claim v1.
	versionMu sync.Mutex

	// heartbeat is how often an SSE stream emits a comment frame. Kept on the
	// session so tests can shorten it.
	heartbeat time.Duration

	opened bool // browser auto-open already attempted
	done   chan struct{}
}

// SessionInfo is the JSON contract returned to the agent. Field names match
// the original Node server so existing skill instructions keep working.
type SessionInfo struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id"`
	Port       int    `json:"port"`
	Host       string `json:"host"`
	URLHost    string `json:"url_host"`
	URL        string `json:"url"`
	ScreenDir  string `json:"screen_dir"`
	StateDir   string `json:"state_dir"`
	SessionDir string `json:"session_dir"`
}

// NewSession creates the session directory layout under root and generates its
// key. root is the directory that holds session directories, not the user's
// project: see resolveLocation for how that is chosen.
func NewSession(root string) (*Session, error) {
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create session root: %w", err)
	}

	// A sortable, human-readable name: someone browsing .visual-companion/
	// should be able to tell when a session happened without decoding an epoch.
	// The random suffix keeps two sessions started in the same second apart.
	id := time.Now().Format("20060102-150405") + "-" + randomSuffix()
	dir := filepath.Join(root, id)

	contentDir := filepath.Join(dir, contentDirName)
	stateDir := filepath.Join(dir, stateDirName)
	assetsDir := filepath.Join(contentDir, assetsDirName)
	for _, d := range []string{contentDir, stateDir, assetsDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}

	key, err := randomKey()
	if err != nil {
		return nil, err
	}

	s := &Session{
		ID:         id,
		Dir:        dir,
		ProjectDir: root,
		urlHost:    "localhost",
		key:        key,
		bus:        newBus(),
		knownFiles: map[string]bool{},
		seenTimes:  map[string]time.Time{},
		eventsPath: filepath.Join(stateDir, "events"),
		infoPath:   filepath.Join(stateDir, "server-info"),
		heartbeat:  heartbeatInterval,
		done:       make(chan struct{}),
	}
	s.scanContentDir() // seed knownFiles so pre-existing screens aren't "new"
	s.generation = 0
	return s, nil
}

// randomSuffix returns four hex characters, enough to disambiguate sessions
// that start within the same second.
func randomSuffix() string {
	buf := make([]byte, 2)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
	}
	return hex.EncodeToString(buf)
}

func randomKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ContentDir is where the agent writes HTML screens.
func (s *Session) ContentDir() string { return filepath.Join(s.Dir, contentDirName) }

// StateDir holds events and server metadata.
func (s *Session) StateDir() string { return filepath.Join(s.Dir, stateDirName) }

// Key returns the session key. It gates every HTTP route.
func (s *Session) Key() string { return s.key }

// Port returns the bound port (valid only after Start).
func (s *Session) Port() int { return s.port }

// URL returns the browser URL including the key.
func (s *Session) URL() string {
	host := s.urlHost
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d/?key=%s", host, s.port, s.key)
}

// Start binds the listener and begins serving. It returns once the server is
// accepting connections.
func (s *Session) Start(host, urlHost string, openBrowser bool) (*SessionInfo, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	if urlHost != "" {
		s.urlHost = urlHost
	} else if host != "127.0.0.1" && host != "localhost" {
		s.urlHost = host
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", host, err)
	}
	s.listener = ln
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		s.port = tcp.Port
	}

	mux := http.NewServeMux()
	s.routes(mux)
	s.httpSrv = &http.Server{Handler: mux}

	go func() {
		_ = s.httpSrv.Serve(ln)
	}()
	go s.watch()

	info := s.Info()
	if err := s.writeInfo(info); err != nil {
		return nil, err
	}
	if openBrowser {
		s.MaybeOpenBrowser()
	}
	return info, nil
}

// Info snapshots the session's connection contract.
func (s *Session) Info() *SessionInfo {
	return &SessionInfo{
		Type:       "server-started",
		SessionID:  s.ID,
		Port:       s.port,
		Host:       s.listener.Addr().String(),
		URLHost:    s.urlHost,
		URL:        s.URL(),
		ScreenDir:  s.ContentDir(),
		StateDir:   s.StateDir(),
		SessionDir: s.Dir,
	}
}

// writeInfo persists the startup JSON so a restarted agent can rediscover the
// session without re-reading tool output.
func (s *Session) writeInfo(info *SessionInfo) error {
	data, err := marshalJSON(info)
	if err != nil {
		return err
	}
	return os.WriteFile(s.infoPath, append(data, '\n'), 0o600)
}

// ReadInfoFile loads a previously written server-info, if present.
func ReadInfoFile(sessionDir string) (*SessionInfo, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, stateDirName, "server-info"))
	if err != nil {
		return nil, err
	}
	var info SessionInfo
	if err := unmarshalJSON(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Close shuts the HTTP server down. The MCP host owns process lifetime, so
// there is no idle timeout or owner-PID watchdog to unwind here.
func (s *Session) Close() error {
	select {
	case <-s.done:
		return nil
	default:
	}
	close(s.done)
	s.bus.closeAll()
	if s.httpSrv != nil {
		return s.httpSrv.Close()
	}
	return nil
}

// --- screen watching -------------------------------------------------------

// screenEntry is one screen file on disk, with the design and version it
// belongs to. The layout is <design>/v<N>/screen.html, so both are derivable
// from the path and the badge can name what the user is looking at.
type screenEntry struct {
	design  string
	version int
	path    string
	mtime   time.Time
}

// describe renders the human-facing identity of a screen.
func (e screenEntry) describe() string {
	if e.design == "" {
		return ""
	}
	return fmt.Sprintf("%s v%d", e.design, e.version)
}

// collectScreens walks the content directory and returns every screen, plus the
// set of paths it saw. Files without a version directory (and anything hidden)
// are ignored — the only supported layout is <design>/v<N>/screen.html.
func (s *Session) collectScreens() ([]screenEntry, map[string]bool) {
	var screens []screenEntry
	seen := map[string]bool{}

	designs, err := os.ReadDir(s.ContentDir())
	if err != nil {
		return nil, seen
	}
	for _, d := range designs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") || d.Name() == assetsDirName {
			continue
		}
		design := d.Name()
		versions, err := os.ReadDir(filepath.Join(s.ContentDir(), design))
		if err != nil {
			continue
		}
		for _, v := range versions {
			if !v.IsDir() || strings.HasPrefix(v.Name(), ".") {
				continue
			}
			n, ok := parseVersionDir(v.Name())
			if !ok {
				continue
			}
			full := filepath.Join(s.ContentDir(), design, v.Name(), screenFileName)
			fi, err := os.Stat(full)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			rel := filepath.Join(design, v.Name(), screenFileName)
			seen[rel] = true
			screens = append(screens, screenEntry{
				design: design, version: n, path: full, mtime: fi.ModTime(),
			})
		}
	}
	return screens, seen
}

// pickNewest returns the most recently written screen, breaking ties by design
// name so the choice is stable rather than dependent on directory order.
func pickNewest(screens []screenEntry) (screenEntry, bool) {
	if len(screens) == 0 {
		return screenEntry{}, false
	}
	sort.Slice(screens, func(i, j int) bool {
		if screens[i].mtime.Equal(screens[j].mtime) {
			return screens[i].path > screens[j].path
		}
		return screens[i].mtime.After(screens[j].mtime)
	})
	return screens[0], true
}

// scanContentDir seeds knownFiles so pre-existing screens are not reported as
// newly added on the first poll.
func (s *Session) scanContentDir() {
	screens, seen := s.collectScreens()
	newest, ok := pickNewest(screens)

	s.mu.Lock()
	s.knownFiles = seen
	if ok {
		s.newestFile = newest.path
		s.current = newest
	}
	s.mu.Unlock()
}

// watch polls for new screens and announces them to connected browsers.
func (s *Session) watch() {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.poll()
		}
	}
}

func (s *Session) poll() {
	screens, current := s.collectScreens()

	var added, updated []string
	for _, e := range screens {
		rel, err := filepath.Rel(s.ContentDir(), e.path)
		if err != nil {
			continue
		}
		s.mu.Lock()
		known := s.knownFiles[rel]
		s.mu.Unlock()

		if !known {
			added = append(added, rel)
			continue
		}
		if fi, err := os.Stat(e.path); err == nil {
			s.mu.Lock()
			prev := s.seenTimes[rel]
			s.mu.Unlock()
			if fi.ModTime().After(prev) {
				updated = append(updated, rel)
			}
		}
	}

	s.mu.Lock()
	deleted := false
	for rel := range s.knownFiles {
		if !current[rel] {
			deleted = true
		}
	}
	s.mu.Unlock()

	if len(added) == 0 && len(updated) == 0 && !deleted {
		return
	}

	// A genuinely new screen starts a new interaction round: a version bumped
	// to v2 is a new question, so stale clicks from v1 must not carry over.
	if len(added) > 0 {
		_ = os.Remove(s.eventsPath)
	}

	times := map[string]time.Time{}
	for _, e := range screens {
		if rel, err := filepath.Rel(s.ContentDir(), e.path); err == nil {
			times[rel] = e.mtime
		}
	}
	newest, ok := pickNewest(screens)

	s.mu.Lock()
	s.knownFiles = current
	s.seenTimes = times
	s.generation++
	gen := s.generation
	if ok {
		s.newestFile = newest.path
		s.current = newest
	} else {
		s.newestFile = ""
		s.current = screenEntry{}
	}
	s.mu.Unlock()

	var kind string
	switch {
	case len(added) > 0:
		kind = "screen-added"
	case len(updated) > 0:
		kind = "screen-updated"
	default:
		kind = "screen-removed"
	}
	s.logEvent(map[string]any{"type": kind, "added": added, "updated": updated, "generation": gen})

	s.bus.broadcast(busMsg{Type: "reload", Generation: gen})
}

// currentScreen returns the path of the screen the browser should show.
func (s *Session) currentScreen() string {
	s.mu.Lock()
	if s.newestFile != "" {
		if _, err := os.Stat(s.newestFile); err == nil {
			s.mu.Unlock()
			return s.newestFile
		}
	}
	s.mu.Unlock()

	// The cached path is gone; re-derive from disk.
	screens, _ := s.collectScreens()
	newest, ok := pickNewest(screens)
	if !ok {
		return ""
	}
	s.mu.Lock()
	s.newestFile = newest.path
	s.current = newest
	s.mu.Unlock()
	return newest.path
}

// currentEntry returns the design/version identity of the visible screen.
func (s *Session) currentEntry() screenEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Generation increments each time the screen set changes. The agent uses it
// to tell which screen a batch of clicks belongs to.
func (s *Session) Generation() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// --- events ----------------------------------------------------------------

// Event is one browser interaction.
type Event struct {
	Type       string `json:"type"`
	Choice     string `json:"choice,omitempty"`
	Value      string `json:"value,omitempty"`
	Text       string `json:"text,omitempty"`
	ID         string `json:"id,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
	Generation int    `json:"generation"`
}

// recordEvent appends an interaction to the session's events file and logs it
// to stdout so the MCP host's transcript also carries the action.
func (s *Session) recordEvent(ev Event) error {
	ev.Generation = s.Generation()
	data, err := marshalJSON(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	s.logEvent(map[string]any{"source": "user-event", "type": ev.Type, "choice": ev.Choice, "value": ev.Value, "text": ev.Text})
	return nil
}

// ReadEvents returns the interactions recorded for the current screen and,
// unless keep is false, clears the file so the next read starts fresh.
func (s *Session) ReadEvents(keep bool) ([]Event, error) {
	var out []Event
	data, err := os.ReadFile(s.eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev Event
		if err := unmarshalJSON([]byte(line), &ev); err != nil {
			continue // tolerate a torn final line
		}
		out = append(out, ev)
	}
	if !keep {
		_ = os.Remove(s.eventsPath)
	}
	return out, nil
}

// logEvent writes one JSON line to the server's stdout log file. stdout is
// owned by the MCP transport, so diagnostics go to state/server.log.
func (s *Session) logEvent(v any) {
	data, err := marshalJSON(v)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(s.StateDir(), "server.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// --- browser auto-open -----------------------------------------------------

// MaybeOpenBrowser launches the user's browser at most once per session, and
// only for a loopback bind. It is opt-in: the agent calls it after the user
// approves the companion.
func (s *Session) MaybeOpenBrowser() {
	s.mu.Lock()
	if s.opened {
		s.mu.Unlock()
		return
	}
	s.opened = true
	host := s.listener.Addr().String()
	s.mu.Unlock()

	if !isLoopback(host) {
		return
	}
	_ = openBrowser(s.URL())
}

func isLoopback(hostPort string) bool {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// openBrowser is overridable in tests.
var openBrowser = func(url string) error {
	return launchBrowser(url)
}

func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

func unmarshalJSON(data []byte, v any) error { return json.Unmarshal(data, v) }
