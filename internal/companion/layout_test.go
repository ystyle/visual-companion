package companion

import (
	"strings"
	"testing"
)

// Large displays were the one viewport nobody checked. Without a capped column
// an option card stretched to the full 3840px of a 4K screen; with only a cap it
// shrank to a small island in an empty field. The frame now caps the column,
// scales type and spacing with the viewport, and centres content vertically
// while there is room. These assertions are about the contract, not pixel
// values: they fail if someone removes a mechanism rather than if they retune a
// number.
func TestFrameContentIsCappedForLargeDisplays(t *testing.T) {
	frame := string(testAssets().Frame)

	cases := []struct {
		name string
		want string
		why  string
	}{
		{
			name: "content column is capped",
			want: "max-width",
			why:  "without a cap, cards stretch to the full width of a 4K screen",
		},
		{
			name: "column is centred",
			want: "margin-inline: auto",
			why:  "a capped column must not hug the left edge",
		},
		{
			name: "vertical slack is distributed",
			want: "margin-block: auto",
			why:  "two options do not fill a 4K viewport; all the slack below reads as broken",
		},
		{
			name: "main is a flex column",
			want: "display: flex; flex-direction: column",
			why:  "margin-block:auto only centres a flex child",
		},
		{
			name: "body copy keeps a measure",
			want: "68ch",
			why:  "unbounded line length is the actual readability problem",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(frame, tc.want) {
				t.Errorf("frame template no longer contains %q — %s", tc.want, tc.why)
			}
		})
	}
}

// Type and controls must scale with the viewport, or the page looks undersized
// on a 2K/4K monitor even once the column is capped.
func TestFrameScalesWithViewport(t *testing.T) {
	frame := string(testAssets().Frame)

	// These are the elements whose size carries the page's visual weight.
	for _, sel := range []string{
		"h2 { font-size: clamp(",
		"h3 { font-size: clamp(",
		".option .content h3 { font-size: clamp(",
		".option .content p { color:",
	} {
		if !strings.Contains(frame, sel) {
			t.Errorf("expected %q to size with the viewport", sel)
		}
	}
	if !strings.Contains(frame, "width: clamp(") || !strings.Contains(frame, "height: clamp(") {
		t.Error("the option letter badge should scale with the viewport too")
	}
	// Padding should scale as well, so the column does not look cramped at 4K.
	if n := strings.Count(frame, "clamp("); n < 8 {
		t.Errorf("only %d clamp() uses found; type, spacing and controls should all scale", n)
	}
}

// Mockups are the tool's main output, not a side case: most screens are
// wireframes. They were missed by the first large-display pass, so every
// wireframe class sat at a fixed rem while headings scaled — a 4K mockup looked
// like a thumbnail pasted onto a large page.
func TestWireframeClassesScaleWithViewport(t *testing.T) {
	frame := string(testAssets().Frame)

	// One entry per class an agent is told it can use for mockups.
	for _, sel := range []string{
		".mockup-header",
		".mockup-body",
		".mock-nav",
		".mock-sidebar",
		".mock-content",
		".mock-button",
		".mock-input",
		".placeholder",
		".pros",
		".cons",
		".card-body",
	} {
		// Classes may be declared alone (".pros {") or grouped (".pros, .cons {").
		idx := strings.Index(frame, sel+" {")
		if idx < 0 {
			idx = strings.Index(frame, sel+",")
		}
		if idx < 0 {
			t.Errorf("class %s disappeared from the frame template", sel)
			continue
		}
		// Look at just this rule block.
		end := strings.Index(frame[idx:], "}")
		if end < 0 {
			t.Errorf("class %s has an unterminated rule", sel)
			continue
		}
		rule := frame[idx : idx+end]
		// A rule that only sets colours is fine; one that sets a size must scale.
		setsSize := strings.Contains(rule, "font-size") ||
			strings.Contains(rule, "padding") || strings.Contains(rule, "min-width")
		if setsSize && !strings.Contains(rule, "clamp(") {
			t.Errorf("class %s sets a size but does not scale with the viewport:\n%s", sel, rule)
		}
	}
}

// Side-by-side mockups must not leave a dead gap under the shorter one.
func TestSplitMockupsAbsorbExtraHeight(t *testing.T) {
	frame := string(testAssets().Frame)
	for _, want := range []string{
		".split > .mockup { display: flex;",
		".split > .mockup > .mockup-body { flex: 1;",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("split mockups no longer fill their stretched cell (%q missing); "+
				"the shorter one will show an empty band", want)
		}
	}
}

// Small screens must not regress: the media query for narrow layouts stays, and
// the column is not given a fixed width that would overflow a phone.
func TestFrameStillWorksOnSmallScreens(t *testing.T) {
	frame := string(testAssets().Frame)
	if !strings.Contains(frame, "@media (max-width: 700px)") {
		t.Error("the narrow-screen breakpoint for .split was removed")
	}
	if strings.Contains(frame, "#frame-content { padding: 2rem; min-height: 100%; }") {
		t.Error("the legacy fixed-padding content rule is back; it does not scale")
	}
}
