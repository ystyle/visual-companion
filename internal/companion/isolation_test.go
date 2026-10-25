package companion

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Two agent conversations share one MCP server process. Before this was fixed, a
// push from either resolved to the globally newest session, so the second
// conversation's page silently replaced the first's mid-review. That happened
// for real: a layout the user was looking at was swapped for an unrelated demo.
//
// The topology is simulated faithfully: one Registry (one process) with two
// independent MCP connections into it — exactly what several conversations
// talking to a single visual-companion binary look like.
func newSharedServerPair(t *testing.T) (*Registry, *mcp.ClientSession, *mcp.ClientSession) {
	t.Helper()

	reg := NewRegistry(testAssets(), t.TempDir(), "127.0.0.1", "", false)
	t.Cleanup(reg.CloseAll)

	connect := func() *mcp.ClientSession {
		server := mcp.NewServer(&mcp.Implementation{Name: "vc-test", Version: "test"}, nil)
		RegisterTools(server, reg, "test")
		st, ct := mcp.NewInMemoryTransports()
		ctx := context.Background()
		if _, err := server.Connect(ctx, st, nil); err != nil {
			t.Fatalf("server connect: %v", err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "test"}, nil)
		cs, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatalf("client connect: %v", err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		return cs
	}

	return reg, connect(), connect()
}

// pushAndServe pushes a screen and returns the page the given URL serves.
func pushAndServe(t *testing.T, cs *mcp.ClientSession, url, html, design string) string {
	t.Helper()
	callTool(t, cs, "push_screen", map[string]any{"html": html, "design": design})
	return fetchScreen(t, url)
}

func TestTwoConversationsDoNotShareASession(t *testing.T) {
	_, csA, csB := newSharedServerPair(t)

	a := callTool(t, csA, "start_companion", nil)
	b := callTool(t, csB, "start_companion", nil)
	if a["session_id"] == b["session_id"] {
		t.Fatalf("both conversations got the same session: %v", a["session_id"])
	}
	if a["url"] == b["url"] {
		t.Fatalf("both conversations got the same URL: %v", a["url"])
	}

	urlA, urlB := a["url"].(string), b["url"].(string)

	// A publishes first. B must not disturb it.
	if body := pushAndServe(t, csA, urlA, "<h2>Conversation A page</h2>", "from-a"); !strings.Contains(body, "Conversation A page") {
		t.Errorf("A's browser does not show A's page: %.200s", body)
	}
	if body := pushAndServe(t, csB, urlB, "<h2>Conversation B page</h2>", "from-b"); !strings.Contains(body, "Conversation B page") {
		t.Errorf("B's browser does not show B's page: %.200s", body)
	}

	// The regression: A's screen still shows A's page after B pushed.
	bodyA := fetchScreen(t, urlA)
	if strings.Contains(bodyA, "Conversation B page") {
		t.Error("B's push leaked into A's browser — the cross-talk bug is back")
	}
	if !strings.Contains(bodyA, "Conversation A page") {
		t.Errorf("A's screen was replaced: %.200s", bodyA)
	}
}

func TestClicksStayWithTheirConversation(t *testing.T) {
	_, csA, csB := newSharedServerPair(t)

	a := callTool(t, csA, "start_companion", nil)
	b := callTool(t, csB, "start_companion", nil) // B must have its own session
	callTool(t, csA, "push_screen", map[string]any{"html": "<h2>A</h2>", "design": "a"})
	callTool(t, csB, "push_screen", map[string]any{"html": "<h2>B</h2>", "design": "b"})

	// Click in B's browser as well, so both sessions hold an event. If
	// resolution falls back to "newest session", A would read B's event and
	// vice versa — the assertions below catch that in both directions.
	postClick(t, a["url"].(string), "from-a", "A choice")
	postClick(t, b["url"].(string), "from-b", "B choice")

	gotA := callTool(t, csA, "get_events", nil)
	gotB := callTool(t, csB, "get_events", nil)
	if gotA["count"].(float64) != 1 || gotB["count"].(float64) != 1 {
		t.Fatalf("each conversation should see exactly its own event: A=%+v B=%+v", gotA, gotB)
	}
	if choiceOf(gotA) != "from-a" {
		t.Errorf("A read the wrong event: %v", choiceOf(gotA))
	}
	if choiceOf(gotB) != "from-b" {
		t.Errorf("B read the wrong event: %v", choiceOf(gotB))
	}
}

// choiceOf returns the choice field of the single event in a get_events result.
func choiceOf(res map[string]any) string {
	events, _ := res["events"].([]any)
	if len(events) != 1 {
		return ""
	}
	ev, _ := events[0].(map[string]any)
	c, _ := ev["choice"].(string)
	return c
}

// Several sessions live, caller never called start_companion: refusing is right.
// Quietly returning the newest is what caused the cross-talk in the first place.
func TestAmbiguousResolveRefusesToGuess(t *testing.T) {
	reg := NewRegistry(testAssets(), t.TempDir(), "127.0.0.1", "", false)
	t.Cleanup(reg.CloseAll)

	if _, err := reg.Start(); err != nil {
		t.Fatalf("start 1: %v", err)
	}
	if _, err := reg.Start(); err != nil {
		t.Fatalf("start 2: %v", err)
	}

	_, err := reg.ResolveForClient("never-bound", "")
	if err == nil {
		t.Fatal("expected an error when several sessions exist and the client is unbound")
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Errorf("the error should point at session_id, got: %v", err)
	}
}

// The common single-agent case must keep working without any binding.
func TestSingleSessionResolvesWithoutBinding(t *testing.T) {
	reg := NewRegistry(testAssets(), t.TempDir(), "127.0.0.1", "", false)
	t.Cleanup(reg.CloseAll)

	info, err := reg.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := reg.ResolveForClient("unbound", ""); err != nil {
		t.Errorf("a lone session should resolve without binding: %v", err)
	}
	if _, err := reg.ResolveForClient("unbound", info.SessionID); err != nil {
		t.Errorf("explicit id should resolve: %v", err)
	}
}

// list_sessions is deliberately global: an agent may need to see every live
// companion, including ones other conversations started.
func TestListSessionsSeesEveryConversation(t *testing.T) {
	_, csA, csB := newSharedServerPair(t)
	callTool(t, csA, "start_companion", nil)
	callTool(t, csB, "start_companion", nil)

	out := callTool(t, csB, "list_sessions", nil)
	if out["count"].(float64) != 2 {
		t.Errorf("list_sessions should report both live sessions, got %+v", out["count"])
	}
}
