package companion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerInstructions is delivered to every MCP client. Upstream kept this
// guidance in a skill file an agent had to go read; putting it in the server's
// instructions means the behavioral contract travels with the tool.
const ServerInstructions = `A browser tab for showing the user things while you talk to them in the terminal.

This is a tool, not a mode. Accepting the companion does not mean every question
goes to the browser — decide per question.

Offer it JUST IN TIME, never upfront. Wait until a question would genuinely be
clearer shown than told (a real mockup, layout, or diagram question — not merely
a UI *topic*), then offer it in its own message with nothing else in it. If no
visual question ever arises, never offer it. If the user declines, stay in the
terminal and do not ask again.

Once it is running, pick the right surface for each question:
  - Browser: mockups, wireframes, layout comparisons, architecture diagrams,
    visual polish, spatial relationships.
  - Terminal: requirements, scope, conceptual choices, tradeoff lists, API
    design, anything whose answer is words rather than a visual preference.

Then: push_screen with an HTML fragment, tell the user what is on screen and
remind them of the URL, and end your turn. On your next turn call get_events to
see what they clicked, and merge that with whatever they typed.

NAME YOUR DESIGNS. push_screen takes a design name; reusing a name publishes the
next version of that design (v1, then v2, ...) instead of overwriting it. Name
each design for what it is — "dashboard-layout", "onboarding-flow" — and push
the same name again every time you revise it. The user sees the design and
version in the browser header and in the directory path, so versions are how
they tell one round from the next. When you are comparing genuinely different
alternatives rather than revising one, give them different design names.

When the conversation returns to terminal-only content, push a short "waiting"
screen so the browser is not left showing a resolved choice.`

// ScreenRequest is the input to push_screen.
type ScreenRequest struct {
	HTML      string `json:"html" jsonschema:"the screen content: an HTML fragment, or a full document if you need complete control"`
	Design    string `json:"design,omitempty" jsonschema:"name of the design this screen belongs to, e.g. 'dashboard-layout' or 'onboarding-flow'. Reuse the same name when revising it: the version increments automatically and the user sees it as v1, v2, ... Use a new name for a different design being compared side by side."`
	Label     string `json:"label,omitempty" jsonschema:"short note about what changed in this version, e.g. 'sidebar moved right'"`
	SessionID string `json:"session_id,omitempty" jsonschema:"session to push to; omit when only one companion is running"`
}

// ScreenResult is the output of push_screen.
type ScreenResult struct {
	Status     string `json:"status"`
	Design     string `json:"design"`
	Version    int    `json:"version"`
	Screen     string `json:"screen"`
	URL        string `json:"url"`
	SessionID  string `json:"session_id"`
	Generation int    `json:"generation"`
}

// EventsRequest is the input to get_events.
type EventsRequest struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"session to read; omit when only one companion is running"`
	Keep      bool   `json:"keep,omitempty" jsonschema:"set true to read without clearing, so the same clicks can be read twice"`
}

// EventsResult is the output of get_events.
type EventsResult struct {
	Status     string  `json:"status"`
	SessionID  string  `json:"session_id"`
	Generation int     `json:"generation"`
	Count      int     `json:"count"`
	Events     []Event `json:"events"`
	Note       string  `json:"note,omitempty"`
}

// RegisterTools attaches the companion's tool surface to an MCP server.
func RegisterTools(server *mcp.Server, reg *Registry, version string) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "start_companion",
		Description: "Start the browser companion and return its URL. Call this only after the user " +
			"has agreed to it, then share the URL with them. Safe to call again: it starts a new session.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		info, err := reg.StartWithClient(ctx, req.Session)
		if err != nil {
			return nil, nil, fmt.Errorf("start companion: %w", err)
		}
		s, err := reg.Resolve(info.SessionID)
		if err != nil {
			return nil, nil, err
		}
		text := fmt.Sprintf(
			"Companion running.\n\nURL: %s\n\nGive the user that complete URL — the ?key= part is the session key and "+
				"the server rejects requests without it. Do not shorten it to a bare host:port.\n\n"+
				"%s\n\n"+
				"Push your first screen with push_screen, then tell the user what is on screen and end your turn.",
			info.URL, s.location.describe())
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, info, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "push_screen",
		Description: "Show a screen in the user's browser. Pass an HTML fragment (no <html>/<head> needed) " +
			"and the server wraps it in a themed frame that provides .options, .cards, .mockup, .split, " +
			".pros-cons and wireframe CSS classes.\n\n" +
			"Name the design you are working on. Reusing a design name publishes the next version of it " +
			"(v1, then v2, ...), which is what you want when revising: nothing is overwritten and the user " +
			"can see which round they are looking at. Comparing two alternatives side by side? Give them " +
			"different design names.\n\n" +
			"After calling, tell the user the design and version on screen, remind them of the URL, and end " +
			"your turn.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ScreenRequest) (*mcp.CallToolResult, ScreenResult, error) {
		var out ScreenResult
		if strings.TrimSpace(in.HTML) == "" {
			return nil, out, fmt.Errorf("html is required")
		}
		s, err := reg.Resolve(in.SessionID)
		if err != nil {
			return nil, out, err
		}

		design := in.Design
		if strings.TrimSpace(design) == "" {
			design = "design"
		}
		entry, err := s.nextScreen(design)
		if err != nil {
			return nil, out, err
		}

		// Capture the generation before writing so we can tell when the
		// watcher has actually taken the new screen into account.
		before := s.Generation()
		if err := os.WriteFile(entry.path, []byte(in.HTML), 0o644); err != nil {
			return nil, out, fmt.Errorf("write screen: %w", err)
		}
		// A new screen starts a new interaction round: the watcher clears the
		// events file. Waiting here means a get_events call on the agent's
		// very next turn sees the new round, not the previous one's clicks.
		s.waitForGeneration(before, 3*time.Second)

		out = ScreenResult{
			Status:     "pushed",
			Design:     entry.design,
			Version:    entry.version,
			Screen:     entry.path,
			URL:        s.URL(),
			SessionID:  s.ID,
			Generation: s.Generation(),
		}
		text := fmt.Sprintf("%s is now on screen (design %q, version %d).\n\nURL: %s\n\n"+
			"Tell the user which design and version they are looking at, remind them of the URL, then end "+
			"your turn. Read their clicks with get_events on your next turn. To revise it, push the same "+
			"design name again and it becomes v%d.",
			entry.describe(), entry.design, entry.version, s.URL(), entry.version+1)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_events",
		Description: "Read the clicks and selections the user made in the browser since the current screen " +
			"was pushed. Returns them oldest-first; the last click is usually the final answer, and a " +
			"wandering sequence can mean hesitation worth asking about. The list is cleared on read unless " +
			"keep is true, and cleared automatically whenever a new screen is pushed.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in EventsRequest) (*mcp.CallToolResult, EventsResult, error) {
		var out EventsResult
		s, err := reg.Resolve(in.SessionID)
		if err != nil {
			return nil, out, err
		}
		events, err := s.ReadEvents(in.Keep)
		if err != nil {
			return nil, out, fmt.Errorf("read events: %w", err)
		}
		events = sortedEvents(events)

		out = EventsResult{
			Status:     "ok",
			SessionID:  s.ID,
			Generation: s.Generation(),
			Count:      len(events),
			Events:     events,
		}
		if len(events) == 0 {
			out.Note = "No browser interactions yet. The user may have answered in the terminal instead — always weigh their typed reply as the primary signal."
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: describeEvents(out)}},
		}, out, nil
	})

	// Session management. A long conversation can accumulate companions, and
	// the agent needs a way to see what is live and where its files went
	// without guessing.
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_sessions",
		Description: "List the companion sessions running in this conversation, newest last. " +
			"Shows each session's id, URL, where its mockups are stored, how many interactions " +
			"are waiting, and which designs it holds at which version. Use it when you are unsure " +
			"which session_id to push to, before reusing a design name, or to tell the user where " +
			"their mockups ended up.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		sessions := reg.ActiveSessions()
		out := ListSessionsResult{Sessions: make([]SessionSummary, 0, len(sessions))}
		for _, s := range sessions {
			pending, _ := s.ReadEvents(true) // keep: listing must not consume clicks

			var designs []DesignSummary
			for _, name := range s.Designs() {
				designs = append(designs, DesignSummary{
					Design:        name,
					LatestVersion: s.latestVersion(name),
					Dir:           filepath.Join(s.ContentDir(), name),
				})
			}

			out.Sessions = append(out.Sessions, SessionSummary{
				SessionID:     s.ID,
				URL:           s.URL(),
				SessionDir:    s.Dir,
				ScreenDir:     s.ContentDir(),
				LocationFrom:  string(s.location.source),
				Generation:    s.Generation(),
				PendingEvents: len(pending),
				Designs:       designs,
			})
		}
		out.Count = len(out.Sessions)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: describeSessions(out, reg.SessionDirOverride())}},
		}, out, nil
	})
}

// ListSessionsResult is the output of list_sessions.
type ListSessionsResult struct {
	Status   string           `json:"status"`
	Count    int              `json:"count"`
	Sessions []SessionSummary `json:"sessions"`
}

// SessionSummary describes one live session.
type SessionSummary struct {
	SessionID     string          `json:"session_id"`
	URL           string          `json:"url"`
	SessionDir    string          `json:"session_dir"`
	ScreenDir     string          `json:"screen_dir"`
	LocationFrom  string          `json:"location_from"`
	Generation    int             `json:"generation"`
	PendingEvents int             `json:"pending_events"`
	Designs       []DesignSummary `json:"designs"`
}

// DesignSummary describes one named design inside a session.
type DesignSummary struct {
	Design        string `json:"design"`
	LatestVersion int    `json:"latest_version"`
	Dir           string `json:"dir"`
}

func describeSessions(res ListSessionsResult, override string) string {
	var b strings.Builder
	if res.Count == 0 {
		b.WriteString("No companion sessions are running. Call start_companion to start one.")
		return b.String()
	}
	fmt.Fprintf(&b, "%d companion session(s), newest last:\n\n", res.Count)
	for i, s := range res.Sessions {
		latest := ""
		if i == res.Count-1 {
			latest = "  <- the one push_screen targets by default"
		}
		fmt.Fprintf(&b, "%d. %s%s\n   URL: %s\n   Mockups: %s (from %s)\n   %d interaction(s) waiting\n",
			i+1, s.SessionID, latest, s.URL, s.ScreenDir, s.LocationFrom, s.PendingEvents)
		if len(s.Designs) == 0 {
			b.WriteString("   No designs pushed yet.\n")
			continue
		}
		b.WriteString("   Designs:\n")
		for _, d := range s.Designs {
			fmt.Fprintf(&b, "     - %s — at v%d (%s)\n", d.Design, d.LatestVersion, d.Dir)
		}
	}
	b.WriteString("\nMockups persist for as long as the host keeps this server running. ")
	if override != "" {
		fmt.Fprintf(&b, "Sessions are pinned to %s by --session-dir.", override)
	} else {
		b.WriteString("Add .superpowers/ to the project's .gitignore so they are not committed.")
	}
	return b.String()
}

func describeEvents(res EventsResult) string {
	if res.Count == 0 {
		return "No browser interactions recorded for the current screen.\n\n" +
			"The user may have replied in the terminal instead — treat their typed message as the primary " +
			"feedback and these events as supplementary."
	}

	clicks := TotalClicks(res.Events)
	var b strings.Builder
	fmt.Fprintf(&b, "%d interaction(s) on screen generation %d.\n\n", clicks, res.Generation)

	// A map of what the user touched, then the order they changed their mind.
	// Repeat clicks stay visible as counts instead of one line each, so
	// "clicked A then B then settled on A" cannot be buried under forty lines.
	counts := map[string]int{}
	var order []string
	for _, ev := range res.Events {
		key := ev.Choice
		if key == "" {
			key = ev.Value
		}
		if key == "" {
			key = "(unlabelled)"
		}
		if _, seen := counts[key]; !seen {
			order = append(order, key)
		}
		n := ev.Count
		if n == 0 {
			n = 1
		}
		counts[key] += n
	}
	for i, key := range order {
		if i > 0 {
			b.WriteString(", ")
		}
		if counts[key] > 1 {
			fmt.Fprintf(&b, "%s x%d", key, counts[key])
		} else {
			b.WriteString(key)
		}
	}
	b.WriteString("\n")

	if len(res.Events) > 1 {
		fmt.Fprintf(&b, "Selection order (%d changes): ", len(res.Events)-1)
		b.WriteString(selectionPath(res.Events, 24))
		b.WriteString("\n")
	}
	if last := finalChoice(res.Events); last != "" {
		fmt.Fprintf(&b, "Currently selected: %s\n", last)
	}
	// Choices are keys like "a" and "b"; the label is what the user actually
	// read on screen. Without this the agent sees a letter and has to guess
	// what it stood for.
	if legend := labelLegend(res.Events); legend != "" {
		fmt.Fprintf(&b, "Labels: %s\n", legend)
	}

	b.WriteString("\nA short selection order means the user knew what they wanted. A long one means they " +
		"went back and forth, which is worth asking about rather than guessing. The user's terminal " +
		"message remains the primary feedback.")
	return b.String()
}

// selectionPath renders the choices in the order they were made.
//
// Consecutive repeats are already collapsed by the server, so what remains is
// the actual sequence of mind-changes. That sequence still gets arbitrarily
// long when someone oscillates between two options, so a period-2 cycle is
// folded into "a -> b x12": the shape of the hesitation survives, the token
// count does not. Anything longer or irregular is truncated from the middle so
// both the starting point and the conclusion are visible.
func selectionPath(events []Event, max int) string {
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		key := ev.Choice
		if key == "" {
			key = ev.Value
		}
		if key == "" {
			key = "?"
		}
		parts = append(parts, key)
	}

	// Fold a repeating 2-cycle: a b a b a b -> a -> b x3
	if len(parts) >= 4 && parts[0] != parts[1] {
		repeats := true
		for i := 2; i < len(parts); i++ {
			if parts[i] != parts[i%2] {
				repeats = false
				break
			}
		}
		if repeats && len(parts)%2 == 0 {
			return fmt.Sprintf("%s -> %s x%d", parts[0], parts[1], len(parts)/2)
		}
	}

	if len(parts) > max {
		head := max / 2
		tail := max - head
		return strings.Join(append(append(append([]string{}, parts[:head]...),
			fmt.Sprintf("... %d more ...", len(parts)-max)), parts[len(parts)-tail:]...), " -> ")
	}
	return strings.Join(parts, " -> ")
}

// labelLegend maps each choice key to its on-screen label, in first-seen order.
func labelLegend(events []Event) string {
	seen := map[string]bool{}
	var parts []string
	for _, ev := range events {
		if ev.Choice == "" || seen[ev.Choice] {
			continue
		}
		label := strings.TrimSpace(ev.Text)
		if label == "" {
			continue
		}
		seen[ev.Choice] = true
		parts = append(parts, fmt.Sprintf("%s = %s", ev.Choice, collapseSpace(label)))
	}
	return strings.Join(parts, ", ")
}

// collapseSpace squeezes an option's full inner text onto one line so a legend
// entry stays readable.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// finalChoice reports the choice the user landed on.
func finalChoice(events []Event) string {
	if len(events) == 0 {
		return ""
	}
	last := events[len(events)-1]
	if last.Choice != "" {
		return last.Choice
	}
	return last.Value
}
