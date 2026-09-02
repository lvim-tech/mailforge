package render

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/config"
)

// TestOptionsWeedsHeaders: NeoMutt's ignore list starts empty, so weeding without one is weeding
// nothing. The blocklist-shaped alternative is what this guards against — `ignore Received:` and a
// list of the X-* headers seen so far goes stale the first time a provider invents another.
func TestOptionsWeedsHeaders(t *testing.T) {
	got := OptionsRC(config.Default())
	for _, want := range []string{
		"ignore *",
		"unignore From: To: Cc: Bcc: Reply-To: Date: Subject:",
		"unhdr_order *",
		"hdr_order From: To: Cc: Bcc: Reply-To: Date: Subject:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("options.rc is missing %q", want)
		}
	}
	// The catch-all must come BEFORE the exceptions: later entries win, so the reverse order
	// hides everything including the headers the next line just asked for.
	if strings.Index(got, "ignore *") > strings.Index(got, "unignore From:") {
		t.Error("`ignore *` must precede the unignore line, or it hides what that line restores")
	}
}
