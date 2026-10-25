package companion

import (
	"strings"
	"testing"
)

// Binding a wildcard address accepts connections on every interface, but the
// URL given to the user must name an address they can dial. 0.0.0.0 is not
// one. Every case below is a bind address an operator may actually pass.
func TestReachableHostFor(t *testing.T) {
	cases := []struct {
		name     string
		bind     string
		explicit string
		wantHost string
		wantNote bool
	}{
		{name: "loopback becomes localhost", bind: "127.0.0.1", wantHost: "localhost"},
		{name: "localhost stays", bind: "localhost", wantHost: "localhost"},
		{name: "IPv6 loopback becomes localhost", bind: "::1", wantHost: "localhost"},
		{name: "a specific LAN address is already reachable", bind: "192.168.1.10", wantHost: "192.168.1.10"},
		{name: "explicit url-host always wins", bind: "0.0.0.0", explicit: "brainstorm.example", wantHost: "brainstorm.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, note := reachableHostFor(tc.bind, tc.explicit)
			if got != tc.wantHost {
				t.Errorf("host = %q, want %q", got, tc.wantHost)
			}
			if tc.wantNote && note == "" {
				t.Error("expected a note explaining the inferred host")
			}
		})
	}
}

// A wildcard bind must never leak into the URL, in any of its spellings.
func TestWildcardBindsNeverAppearInURL(t *testing.T) {
	for _, bind := range []string{"0.0.0.0", "::", "[::]", "*", ""} {
		host, _ := reachableHostFor(bind, "")
		if isWildcardHost(host) {
			t.Errorf("bind %q produced a wildcard URL host %q", bind, host)
		}
		if host == "" {
			t.Errorf("bind %q produced an empty URL host", bind)
		}
	}
}

// Without an explicit --url-host, a wildcard bind on a machine with a routable
// address should advertise that address and say so.
func TestWildcardBindExplainsItself(t *testing.T) {
	host, note := reachableHostFor("0.0.0.0", "")
	if ip := primaryOutboundIP(); ip != "" {
		if host != ip {
			t.Errorf("host = %q, want the outbound address %q", host, ip)
		}
		if !strings.Contains(note, ip) {
			t.Errorf("note should mention %q, got %q", ip, note)
		}
		if !strings.Contains(note, "--url-host") {
			t.Errorf("note should mention the override, got %q", note)
		}
		return
	}
	// No routable interface (CI sandbox, offline loopback-only host).
	if host != "localhost" {
		t.Errorf("host = %q, want the localhost fallback", host)
	}
	if !strings.Contains(note, "--url-host") {
		t.Errorf("fallback should tell the operator how to fix it, got %q", note)
	}
}

func TestIsWildcardHost(t *testing.T) {
	for _, h := range []string{"0.0.0.0", "::", "[::]", "*", "", " 0.0.0.0 "} {
		if !isWildcardHost(h) {
			t.Errorf("isWildcardHost(%q) = false, want true", h)
		}
	}
	for _, h := range []string{"127.0.0.1", "localhost", "192.168.1.1", "::1", "example.com"} {
		if isWildcardHost(h) {
			t.Errorf("isWildcardHost(%q) = true, want false", h)
		}
	}
}

// hostForURL must bracket IPv6 literals or the URL does not parse.
func TestHostForURL(t *testing.T) {
	cases := map[string]string{
		"localhost":    "localhost",
		"192.168.1.10": "192.168.1.10",
		"::1":          "[::1]",
		"fe80::1":      "[fe80::1]",
		"[::1]":        "[::1]", // already bracketed, untouched
	}
	for in, want := range cases {
		if got := hostForURL(in); got != want {
			t.Errorf("hostForURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The end-to-end contract: a wildcard-bound session hands out a URL the user
// can open, and says why it chose that host.
func TestWildcardSessionAdvertisesReachableURL(t *testing.T) {
	s, err := NewSession(t.TempDir())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.SetAssets(testAssets())
	defer s.Close()

	info, err := s.Start("0.0.0.0", "", false)
	if err != nil {
		t.Fatalf("start on wildcard: %v", err)
	}
	if strings.Contains(info.URL, "0.0.0.0") {
		t.Fatalf("URL advertises the wildcard address: %s", info.URL)
	}
	if isWildcardHost(info.URLHost) {
		t.Errorf("url_host = %q, want a dialable host", info.URLHost)
	}
	if info.URLHostNote == "" {
		t.Error("a wildcard bind should explain the inferred host")
	}
	if !strings.Contains(info.URL, "?key=") {
		t.Errorf("URL lost its session key: %s", info.URL)
	}
}

// An explicit --url-host must survive verbatim, including a name that does not
// resolve from here (reverse proxies, tunnels, split-horizon DNS).
func TestExplicitURLHostIsHonouredOnWildcardBind(t *testing.T) {
	s, err := NewSession(t.TempDir())
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	s.SetAssets(testAssets())
	defer s.Close()

	info, err := s.Start("0.0.0.0", "brainstorm.internal", false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if info.URLHost != "brainstorm.internal" {
		t.Errorf("url_host = %q, want the explicit value", info.URLHost)
	}
	if !strings.HasPrefix(info.URL, "http://brainstorm.internal:") {
		t.Errorf("URL = %q, want it to use the explicit host", info.URL)
	}
	if info.URLHostNote != "" {
		t.Errorf("an explicit host needs no explanation, got %q", info.URLHostNote)
	}
}
