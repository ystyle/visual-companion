package companion

import (
	"strings"
	"testing"
)

// postMultiClick simulates what helper.js sends from a multi-select container:
// the click plus the full set ticked at that moment.
func postMultiClick(t *testing.T, keyedURL, choice, text string, selected []string) {
	t.Helper()
	ev := map[string]any{"type": "click", "choice": choice, "text": text}
	if selected != nil {
		ev["selected"] = selected
	}
	postEvent(t, keyedURL, ev)
}

func TestMultiSelectReportsTheAuthoritativeSet(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html": `<div class="options" data-multiselect>` +
			`<div class="option" data-choice="a">A</div>` +
			`<div class="option" data-choice="b">B</div>` +
			`<div class="option" data-choice="c">C</div></div>`,
		"design": "features",
	})

	// Tick A, tick C, then untick C. A bare click stream cannot express the
	// last step: the event for C looks the same as ticking it.
	postMultiClick(t, url, "a", "A", []string{"a"})
	postMultiClick(t, url, "c", "C", []string{"a", "c"})
	postMultiClick(t, url, "c", "C", []string{"a"})

	out, text := callToolText(t, cs, "get_events", nil)
	if out["count"].(float64) != 3 {
		t.Fatalf("unticking must be its own event, got %+v\n%s", out["count"], text)
	}
	if !strings.Contains(text, "Currently ticked: a\n") {
		t.Errorf("the final set must be reported as just a, got:\n%s", text)
	}
	if strings.Contains(text, "Currently ticked: a, c") {
		t.Errorf("a stale set leaked into the conclusion:\n%s", text)
	}
	// The last click was on C, but C is no longer ticked. Reporting it as the
	// selection would contradict the set and mislead the agent.
	if strings.Contains(text, "Currently selected: c") {
		t.Errorf("multi-select must not report the last click as the selection:\n%s", text)
	}
}

// Unticking everything is a real answer and must be stated, not omitted.
func TestUntickingEverythingIsReported(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html":   `<div class="options" data-multiselect><div class="option" data-choice="a">A</div></div>`,
		"design": "features",
	})

	postMultiClick(t, url, "a", "A", []string{"a"})
	postMultiClick(t, url, "a", "A", []string{})

	_, text := callToolText(t, cs, "get_events", nil)
	if !strings.Contains(text, "Currently ticked: (nothing)") {
		t.Errorf("an empty selection is a decision and must be said out loud:\n%s", text)
	}
}

// The compression must not merge two clicks that leave the selection in
// different states, or unticking an option would vanish entirely.
func TestMultiSelectTogglesAreNotMerged(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html":   `<div class="options" data-multiselect><div class="option" data-choice="a">A</div></div>`,
		"design": "features",
	})

	postMultiClick(t, url, "a", "A", []string{"a"})
	postMultiClick(t, url, "a", "A", []string{}) // untick: same choice, different state

	out := callTool(t, cs, "get_events", nil)
	if out["count"].(float64) != 2 {
		t.Errorf("clicking the same option with a different resulting set is a new event, got %+v", out)
	}
}

// Genuine repeats in a multi-select container still collapse.
func TestMultiSelectRepeatsStillCollapse(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html":   `<div class="options" data-multiselect><div class="option" data-choice="a">A</div></div>`,
		"design": "features",
	})

	for i := 0; i < 5; i++ {
		postMultiClick(t, url, "a", "A", []string{"a"})
	}
	out, text := callToolText(t, cs, "get_events", nil)
	if out["count"].(float64) != 1 {
		t.Fatalf("five identical clicks should be one run, got %+v", out["count"])
	}
	if !strings.Contains(text, "5 interaction") {
		t.Errorf("the count must survive, got:\n%s", text)
	}
}

// Single-select is unchanged: the click's own choice is the selection, so no
// redundant "Currently ticked" line should appear.
func TestSingleSelectHasNoRedundantSetLine(t *testing.T) {
	cs, _ := newTestPair(t)
	start := callTool(t, cs, "start_companion", nil)
	url := start["url"].(string)
	callTool(t, cs, "push_screen", map[string]any{
		"html":   `<div class="options"><div class="option" data-choice="a">A</div></div>`,
		"design": "layout",
	})

	postMultiClick(t, url, "a", "A", nil) // no selected set, as single-select sends
	_, text := callToolText(t, cs, "get_events", nil)
	if strings.Contains(text, "Currently ticked") {
		t.Errorf("single-select should not emit a separate ticked line:\n%s", text)
	}
	if !strings.Contains(text, "Currently selected: a") {
		t.Errorf("single-select should still state the selection:\n%s", text)
	}
}
