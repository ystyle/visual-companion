package companion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepeatClicksCollapseIntoOneRun: clicking the same choice again is one
// decision held, not a new decision. Before this, an undecided user produced
// hundreds of lines the agent had to read through to find the two that
// mattered.
func TestRepeatClicksCollapseIntoOneRun(t *testing.T) {
	cs, reg := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html": `<div class="options"><div class="option" data-choice="a">A</div>` +
			`<div class="option" data-choice="b">B</div></div>`,
		"design": "layout",
	})

	for i := 0; i < 30; i++ {
		postClick(t, url, "b", "Option b")
	}

	// Inspect the audit trail before reading the round: a read consumes it.
	s, _ := reg.Resolve("")
	data, err := os.ReadFile(filepath.Join(s.StateDir(), "events"))
	if err != nil {
		t.Fatalf("events file: %v", err)
	}

	out, text := callToolText(t, cs, "get_events", nil)
	if out["count"].(float64) != 1 {
		t.Fatalf("30 identical clicks should be one run, got %+v", out["count"])
	}
	if !strings.Contains(text, "30 interaction") {
		t.Errorf("the run should still report 30 interactions, got:\n%s", text)
	}
	if !strings.Contains(text, "b x30") {
		t.Errorf("the run should show its count, got:\n%s", text)
	}

	var stored Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &stored); err != nil {
		t.Fatalf("events file is not one JSON object: %s", data)
	}
	if stored.Count != 30 {
		t.Errorf("stored count = %d, want 30", stored.Count)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n"); lines != 0 {
		t.Errorf("expected a single stored run, got %d extra lines", lines)
	}
}

// TestChangingYourMindIsNeverCollapsed is the other half of the bargain: the
// compression must not eat the hesitation signal.
func TestChangingYourMindIsNeverCollapsed(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html": `<div class="options"><div class="option" data-choice="a">A</div>` +
			`<div class="option" data-choice="b">B</div></div>`,
		"design": "layout",
	})

	// Interleave repeats with changes: a a b b b a
	postClick(t, url, "a", "Option a")
	postClick(t, url, "a", "Option a")
	postClick(t, url, "b", "Option b")
	postClick(t, url, "b", "Option b")
	postClick(t, url, "b", "Option b")
	postClick(t, url, "a", "Option a")

	out, text := callToolText(t, cs, "get_events", nil)
	// a x2, b x3, a x1  -> three runs
	if out["count"].(float64) != 3 {
		t.Fatalf("expected 3 runs for a,a,b,b,b,a; got %+v\n%s", out["count"], text)
	}
	if !strings.Contains(text, "6 interaction") {
		t.Errorf("total interactions should still be 6, got:\n%s", text)
	}
	if !strings.Contains(text, "a -> b -> a") {
		t.Errorf("every change of mind must be preserved, got:\n%s", text)
	}
}

// TestOscillationFoldsButStaysVisible: 24 alternating clicks are one fact —
// "they went back and forth a lot" — and must not arrive as 24 tokens.
func TestOscillationFoldsButStaysVisible(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html": `<div class="options"><div class="option" data-choice="a">A</div>` +
			`<div class="option" data-choice="b">B</div></div>`,
		"design": "layout",
	})

	for i := 0; i < 12; i++ {
		postClick(t, url, "a", "Option a")
		postClick(t, url, "b", "Option b")
	}

	_, text := callToolText(t, cs, "get_events", nil)
	if !strings.Contains(text, "a -> b x12") {
		t.Errorf("an oscillation should fold to its shape, got:\n%s", text)
	}
	if !strings.Contains(text, "24 interaction") {
		t.Errorf("the true interaction count must survive compression, got:\n%s", text)
	}
	if !strings.Contains(text, "Currently selected: b") {
		t.Errorf("the conclusion must be stated, got:\n%s", text)
	}
	// The whole report should stay short enough to read at a glance.
	if len(text) > 600 {
		t.Errorf("report is %d chars for 24 clicks; compression is not doing its job:\n%s", len(text), text)
	}
}

// TestSelectionOrderTruncatesFromTheMiddle keeps both ends of a long irregular
// sequence: where the user started and where they landed.
func TestSelectionOrderTruncatesFromTheMiddle(t *testing.T) {
	events := []Event{}
	for i := 0; i < 60; i++ {
		events = append(events, Event{Choice: string(rune('a' + i%20))})
	}
	got := selectionPath(events, 24)
	if !strings.Contains(got, "more ...") {
		t.Errorf("a long irregular sequence should truncate: %s", got)
	}
	if !strings.HasPrefix(got, string(rune('a'+0))) {
		t.Errorf("the start should survive: %s", got)
	}
}

// TestSelectionPathHandlesShortSequences: folding must not damage the ordinary
// case.
func TestSelectionPathHandlesShortSequences(t *testing.T) {
	cases := []struct {
		choices []string
		want    string
	}{
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a -> b"},
		{[]string{"a", "b", "c"}, "a -> b -> c"},
		// Three alternating is not yet a full cycle; do not fold it.
		{[]string{"a", "b", "a"}, "a -> b -> a"},
		{[]string{"a", "b", "a", "b"}, "a -> b x2"},
	}
	for _, tc := range cases {
		events := make([]Event, 0, len(tc.choices))
		for _, c := range tc.choices {
			events = append(events, Event{Choice: c})
		}
		if got := selectionPath(events, 24); got != tc.want {
			t.Errorf("selectionPath(%v) = %q, want %q", tc.choices, got, tc.want)
		}
	}
}

// TestReadStillConsumesTheRound: compression must not turn reads into a replay.
func TestReadStillConsumesTheRound(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{"html": "<h2>x</h2>", "design": "d"})
	postClick(t, url, "a", "A")
	postClick(t, url, "a", "A")

	first := callTool(t, cs, "get_events", nil)
	if first["count"].(float64) != 1 {
		t.Fatalf("expected one run, got %+v", first)
	}
	second := callTool(t, cs, "get_events", nil)
	if second["count"].(float64) != 0 {
		t.Errorf("a read must consume the round, got %+v", second)
	}
}
