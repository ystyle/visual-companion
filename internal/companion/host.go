package companion

import (
	"fmt"
	"net"
	"strings"
)

// Hosts that mean "listen on every interface" rather than "reach me here".
//
// A bind address and a reachable address are different things. Binding 0.0.0.0
// is how you accept connections from another machine, but the URL handed to the
// user must name an address that machine can actually dial — 0.0.0.0 is not
// one. Confusing the two produces a URL that fails to open for everyone, which
// is exactly what a remote or containerised setup hits first.
func isWildcardHost(host string) bool {
	switch strings.TrimSpace(host) {
	case "", "0.0.0.0", "::", "[::]", "*":
		return true
	}
	return false
}

// isLoopbackHost reports whether the host only accepts local connections.
func isLoopbackHost(host string) bool {
	h := strings.Trim(host, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// reachableHostFor picks the hostname to put in the browser URL.
//
// Precedence:
//  1. An explicit --url-host always wins; the operator knows their network.
//  2. A non-wildcard bind address is already reachable, so use it.
//  3. A wildcard bind means "all interfaces", so pick the machine's primary
//     outbound address — the one another host on the network can dial.
//  4. If no address can be determined, fall back to localhost so the URL at
//     least works on this machine, and say so in the returned note.
func reachableHostFor(bindHost, explicitURLHost string) (host, note string) {
	if explicitURLHost != "" {
		return explicitURLHost, ""
	}
	if !isWildcardHost(bindHost) {
		if isLoopbackHost(bindHost) {
			return "localhost", ""
		}
		return bindHost, ""
	}
	if ip := primaryOutboundIP(); ip != "" {
		return ip, fmt.Sprintf(
			"Bound to all interfaces; the URL uses this machine's address %s. "+
				"Pass --url-host to advertise a different hostname or IP.", ip)
	}
	return "localhost", "Bound to all interfaces but could not determine a LAN address; " +
		"the URL falls back to localhost and will only work on this machine. " +
		"Pass --url-host with a reachable address."
}

// primaryOutboundIP returns the address the OS would use to reach the outside
// world. It sends no packets: dialing a UDP address only selects a route.
func primaryOutboundIP() string {
	// An unroutable destination is fine for a route lookup; nothing is sent.
	conn, err := net.Dial("udp", "192.0.2.1:9") // RFC 5737 TEST-NET-1
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
		if addr.IP.IsLoopback() || addr.IP.IsUnspecified() {
			return ""
		}
		return addr.IP.String()
	}
	return ""
}

// hostForURL renders a host for interpolation into a URL, bracketing IPv6
// literals so the result parses.
func hostForURL(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}
