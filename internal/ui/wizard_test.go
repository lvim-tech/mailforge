package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
)

// TestCollectFoldersKeepsTheDelimiter locks the bug this function was extracted for.
//
// Delim is learned from the server's LIST reply during verification and has no input on the form.
// The folders step used to rebuild Folders as a literal, which reset it — and the account then
// rendered "set record = +INBOX.Sent", a path that exists on the server and nowhere on this disk.
func TestCollectFoldersKeepsTheDelimiter(t *testing.T) {
	v := newAccountsView(config.Default())
	v.draft.Folders = account.Folders{
		Delim: ".",
		Sent:  "INBOX.Sent",
	}
	v.fSent.set("INBOX.Sent")
	v.fDrafts.set("INBOX.Drafts")
	v.fTrash.set("INBOX.Trash")
	v.fSpam.set("INBOX.spam")
	v.fArchive.set("INBOX.Archive")

	v.collectFolders()

	if v.draft.Folders.Delim != "." {
		t.Fatalf("the delimiter was lost: %q", v.draft.Folders.Delim)
	}
	// And with it kept, the translation the whole thing exists for works.
	if got := v.draft.LocalFolder(v.draft.Folders.Sent); got != "Sent" {
		t.Errorf("INBOX.Sent translated to %q, want Sent", got)
	}
	if got := v.draft.LocalFolders().Trash; got != "Trash" {
		t.Errorf("trash translated to %q, want Trash", got)
	}
}

// TestCollectFoldersTakesWhatTheFormSays: the five names are the form's to change, and blanks mean
// the account has no such mailbox rather than "leave the old one".
func TestCollectFoldersTakesWhatTheFormSays(t *testing.T) {
	v := newAccountsView(config.Default())
	v.draft.Folders = account.Folders{Delim: "/", Sent: "old", Archive: "gone"}
	v.fSent.set("  Sent  ")
	v.fArchive.set("")

	v.collectFolders()

	if v.draft.Folders.Sent != "Sent" {
		t.Errorf("the typed name did not win, or was not trimmed: %q", v.draft.Folders.Sent)
	}
	if v.draft.Folders.Archive != "" {
		t.Errorf("a cleared field left the old value: %q", v.draft.Folders.Archive)
	}
	if v.draft.Folders.Delim != "/" {
		t.Errorf("the delimiter was lost: %q", v.draft.Folders.Delim)
	}
}

// TestAnEmptyFieldKeepsItsHintWhenFocused is the bug the hints caused.
//
// The hints name plausible values, so an empty field reads as filled in. Dropping the hint the
// moment the cursor arrived made focusing look like deletion — the value appeared to vanish.
func TestAnEmptyFieldKeepsItsHintWhenFocused(t *testing.T) {
	in := newInput("IMAP host", "required — imap.example.com")
	for _, focused := range []bool{false, true} {
		if got := in.render(focused); !strings.Contains(got, "imap.example.com") {
			t.Errorf("focused=%v: the hint is gone:\n%q", focused, got)
		}
	}
	// And once there IS a value, the hint must not be shown beside it.
	in.set("mail.example.org")
	for _, focused := range []bool{false, true} {
		if got := in.render(focused); strings.Contains(got, "imap.example.com") {
			t.Errorf("focused=%v: the hint was drawn next to a real value:\n%q", focused, got)
		}
	}
}

// TestARequiredFieldSaysSo: the optional hints describe what an empty field becomes ("empty = 993"),
// which is true. A required field must not use that shape, because there is no value to become.
func TestARequiredFieldSaysSo(t *testing.T) {
	v := newAccountsView(config.Default())
	for _, in := range []input{v.fAddress, v.fIHost, v.fSHost} {
		if !strings.HasPrefix(in.hint, "required") {
			t.Errorf("%s is required but its hint reads %q", in.label, in.hint)
		}
	}
	for _, in := range []input{v.fIPort, v.fISec, v.fIUser, v.fSPort, v.fSSec, v.fSUser} {
		if strings.HasPrefix(in.hint, "required") {
			t.Errorf("%s is optional but its hint claims otherwise: %q", in.label, in.hint)
		}
	}
}

// TestEditingAFieldReplacesThenCorrects is the rule the edit forms depend on.
//
// Prefilled values were set rather than seeded, which made every field append-only: changing a
// twenty-character hostname meant twenty backspaces, and nothing on screen mentioned ctrl+u.
func TestEditingAFieldReplacesThenCorrects(t *testing.T) {
	key := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	// Typing over a seeded value replaces it.
	in := newInput("IMAP host", "")
	in.seed("eu108.jethosting.com")
	in.update(key("m"))
	in.update(key("x"))
	if in.String() != "mx" {
		t.Errorf("typing appended instead of replacing: %q", in.String())
	}

	// Moving first means "correct this", and the value survives.
	in2 := newInput("IMAP host", "")
	in2.seed("eu108.jethosting.com")
	in2.update(tea.KeyMsg{Type: tea.KeyLeft})
	in2.update(key("X"))
	if in2.String() != "eu108.jethosting.coXm" {
		t.Errorf("moving first did not keep the value: %q", in2.String())
	}

	// And a field filled by discovery is NOT seeded — nothing was typed to be replaced, and a
	// stray key must not wipe a host the user is about to accept.
	in3 := newInput("IMAP host", "")
	in3.set("mail.example.com")
	in3.update(key("!"))
	if in3.String() != "mail.example.com!" {
		t.Errorf("a set value behaved as seeded: %q", in3.String())
	}
}

// TestASeededFieldLooksSelected: the replace-on-type rule is only fair if it is visible.
//
// The colour profile is forced, because the styles collapse to bare text when the tests run without
// a terminal — and then this check passes for a version that draws nothing at all.
func TestASeededFieldLooksSelected(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	in := newInput("IMAP host", "")
	in.seed("eu108.jethosting.com")
	seeded := in.render(true)
	in.update(tea.KeyMsg{Type: tea.KeyLeft}) // no longer fresh, same value
	if plain := in.render(true); plain == seeded {
		t.Error("a seeded field is drawn exactly like an ordinary one")
	}
	if !strings.Contains(seeded, "eu108.jethosting.com") {
		t.Errorf("the seeded value is not drawn at all: %q", seeded)
	}
}
