package ui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/setup"
)

// The signature is edited in $EDITOR, not in a field of this program.
//
// It is the only piece of an account that is a BLOCK of text — several lines, blank lines between
// them, sometimes a rule of dashes — and a one-line input cannot hold that. The alternatives were a
// text area written from scratch inside a settings program, or the editor the user already has
// configured, already knows, and is about to write every message in. The second is not a shortcut;
// it is the right tool arriving by the shortest path.
//
// The TEXT is the registry's, not the file's. mailforge renders the file from the account exactly
// as it renders every other artefact, which is what lets the doctor notice a signature that was
// edited outside the program or lost entirely — a file-only signature would be the one thing
// nothing could check.

type signatureEdited struct {
	address string
	text    string
	err     error
}

// editSignature opens the account's signature in $EDITOR and comes back with what was written.
func (v *accountsView) editSignature(a account.Account) tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		// Not guessed. A settings program that silently opens something the user did not
		// choose is a program that opens vi on a machine where nobody knows how to leave it.
		return failure("no $EDITOR set — export one, or write the file by hand: %s", a.SigFile(v.cfg))
	}

	// A scratch file rather than the real one: the editor may be killed, may write half a
	// buffer, may be quit without saving. Whatever comes back is a candidate, and the artefact
	// NeoMutt reads is only rewritten once it has been recorded in the registry.
	tmp, err := os.CreateTemp("", "mailforge-sig-*.txt")
	if err != nil {
		return failure("%v", err)
	}
	path := tmp.Name()
	if _, err := tmp.WriteString(a.Signature); err != nil {
		_ = tmp.Close()
		return failure("%v", err)
	}
	if err := tmp.Close(); err != nil {
		return failure("%v", err)
	}

	// The editor gets the terminal to itself; bubbletea puts the screen back afterwards.
	cmd := exec.Command(editor, path) // #nosec G204 — the editor is the user's own $EDITOR
	addr := a.Address
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		defer func() { _ = os.Remove(path) }()
		if runErr != nil {
			return signatureEdited{address: addr, err: runErr}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return signatureEdited{address: addr, err: err}
		}
		return signatureEdited{address: addr, text: string(b)}
	})
}

// applySignature records the edited text and rewrites what NeoMutt reads.
//
// Through ApplyAccount rather than through a shortcut that touches only the two files involved:
// there is one way to write an account in this program, it is the one the plan screen and the
// doctor are written against, and a second path would be a second set of steps to keep in step.
// The extra work is inert — every other artefact reports "already up to date".
func (v *accountsView) applySignature(msg signatureEdited) tea.Cmd {
	if msg.err != nil {
		return failure("%v", msg.err)
	}
	a, ok := v.reg.Find(msg.address)
	if !ok {
		return failure("%s is no longer in the registry", msg.address)
	}
	text := strings.TrimRight(msg.text, "\n \t")
	if text == a.Signature {
		return status("signature unchanged")
	}
	a.Signature = text

	// The plan screen, the same one an apply from the wizard ends on. Writing several files in
	// somebody's home directory and saying nothing but "done" is the thing this program was
	// built not to do.
	v.mode = mApply
	v.busy = "writing the signature"
	cfg, reg := v.cfg, v.reg
	return func() tea.Msg {
		// No secret: ApplyAccount leaves the stored password alone unless one is handed to it.
		return applied(setup.ApplyAccount(cfg, &reg, a, nil))
	}
}
