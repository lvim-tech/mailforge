package render

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/config"
)

/*
The two halves of showing HTML mail have to be written together or not at all.

mailforge shipped one of them — a mailcap rule — and not the other, and without auto_view the rule
is never reached for the body: every HTML message opened empty, for months, with the rule sitting
in place the whole time. These assert the pair, and that neither appears when there is nothing to
run.
*/
func TestOptionsWritesBothHalvesOrNeither(t *testing.T) {
	c := config.Default()
	c.NeomuttDir = "/home/x/.config/neomutt"

	c.HTMLViewer = config.HTMLViewerNone
	out := OptionsRC(c)
	if strings.Contains(out, "auto_view text/html") {
		t.Error("auto_view written with no viewer to run")
	}
	if !strings.Contains(out, "no HTML viewer available") {
		t.Error("silence about it: nothing here and no opinion look the same on disk")
	}
	if body := MailcapRC(c); strings.Contains(body, "text/html;") {
		t.Errorf("a rule with no program behind it:\n%s", body)
	}

	// A viewer that resolves writes all three: the path naming both files, auto_view, and the
	// preference for the text part.
	if program, _ := c.HTMLViewerCommand(); program != "" {
		t.Fatalf("HTMLViewerNone resolved to %q", program)
	}
	c.HTMLViewer = config.HTMLViewerAuto
	program, command := c.HTMLViewerCommand()
	if program == "" {
		t.Skip("neither w3m nor lynx is installed here, so there is nothing to resolve to")
	}
	out = OptionsRC(c)
	for _, want := range []string{
		"set mailcap_path = /home/x/.config/neomutt/mailcap:/home/x/.config/neomutt/mailforge/mailcap",
		"auto_view text/html",
		"alternative_order text/plain text/enriched text/html",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("options.rc is missing %q", want)
		}
	}
	if body := MailcapRC(c); !strings.Contains(body, "text/html; "+command+"; nametemplate=%s.html; copiousoutput") {
		t.Errorf("the mailcap rule does not run the resolved viewer:\n%s", body)
	}
}

// The user's mailcap comes first because mailcap takes the FIRST match — the opposite of how
// NeoMutt reads its source lines, and easy to write the wrong way round.
func TestTheUserMailcapIsNamedFirst(t *testing.T) {
	c := config.Default()
	c.NeomuttDir = "/n"
	line := NeomuttMailcapPath(c)
	user, managed := strings.Index(line, "/n/mailcap"), strings.Index(line, "/n/mailforge/mailcap")
	if user < 0 || managed < 0 {
		t.Fatalf("both files should be named: %q", line)
	}
	if user > managed {
		t.Errorf("mailforge's mailcap is read before the user's, so theirs cannot win: %q", line)
	}
}

// A named viewer is a decision. It is not quietly replaced by the other one when it is missing;
// the doctor reports it instead. Silently running something else is how a program comes to do
// what its settings do not say.
func TestANamedViewerIsNotSwappedForTheOther(t *testing.T) {
	c := config.Default()
	c.HTMLViewer = "definitely-not-installed"
	if program, _ := c.HTMLViewerCommand(); program != "" {
		t.Errorf("an unknown viewer resolved to %q", program)
	}
	if got := c.HTMLViewerResolved(); !strings.Contains(got, "cannot be shown") {
		t.Errorf("the setting screen would not say it is broken: %q", got)
	}
}
