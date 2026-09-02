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

// TestNoKeptHeadersMeansNoWeeding: an empty list is a decision — show everything — and must not
// emit `ignore *`, which with nothing unignored after it would hide every header instead.
func TestNoKeptHeadersMeansNoWeeding(t *testing.T) {
	c := config.Default()
	c.KeepHeaders = []string{}
	got := OptionsRC(c)
	if strings.Contains(got, "ignore *") {
		t.Error("an empty keep list still emitted `ignore *`, which hides every header")
	}
	if !strings.Contains(got, "# no headers kept in mailforge") {
		t.Error("an empty keep list should say so, not leave the reader guessing")
	}
}

// TestKeptHeadersComeFromTheConfig — the setting has to actually reach the file, and the same list
// has to drive both the unignore and the ordering, or headers are kept in one order and shown in
// another.
func TestKeptHeadersComeFromTheConfig(t *testing.T) {
	c := config.Default()
	c.KeepHeaders = []string{"Subject:", "From:", "List-Id:"}
	got := OptionsRC(c)
	for _, want := range []string{
		"unignore Subject: From: List-Id:",
		"hdr_order Subject: From: List-Id:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("options.rc is missing %q", want)
		}
	}
}

// TestBlankHeaderEntriesAreDropped: the list is edited as a line of text, and an empty entry
// becomes a pattern NeoMutt matches against every header — which would undo the `ignore *` above it.
func TestBlankHeaderEntriesAreDropped(t *testing.T) {
	c := config.Default()
	c.KeepHeaders = []string{"From:", "  ", "", "Date:"}
	if got, want := OptionsRC(c), "unignore From: Date:\n"; !strings.Contains(got, want) {
		t.Errorf("options.rc is missing %q", want)
	}
}
