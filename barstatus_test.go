package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/inbox"
)

// capture runs fn with stdout redirected, and returns what it wrote.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	b := make([]byte, 4096)
	n, _ := r.Read(b)
	r.Close()
	return string(b[:n])
}

func decode(t *testing.T, s string) barStatus {
	t.Helper()
	var got barStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &got); err != nil {
		t.Fatalf("not valid JSON (%v): %q", err, s)
	}
	return got
}

// TestBarJSONSplitsInboxFromSpam is the reason Count carries two numbers. A single total cannot
// distinguish three people waiting for an answer from three pieces of junk, and those call for
// different reactions from whoever glances at the bar.
func TestBarJSONSplitsInboxFromSpam(t *testing.T) {
	per := []inbox.Count{
		{Address: "a@example.com", Inbox: 1, Spam: 2},
		{Address: "b@example.com", Inbox: 0, Spam: 1},
	}
	got := decode(t, capture(t, func() { printBarJSON(4, per, nil) }))
	if got.Text != "1/3 Unread" {
		t.Errorf("text = %q, want %q", got.Text, "1/3 Unread")
	}
	if got.Class != "unread" || got.Alt != "unread" {
		t.Errorf("class %q / alt %q, want both %q", got.Class, got.Alt, "unread")
	}
	// Per account, split the same way — the tooltip is where you look to find WHICH mailbox.
	for _, want := range []string{"1/2\ta@example.com", "0/1\tb@example.com"} {
		if !strings.Contains(got.Tooltip, want) {
			t.Errorf("tooltip is missing %q: %q", want, got.Tooltip)
		}
	}
}

// TestBarJSONSaysNoUnreadInWords: a bar is read from the corner of an eye, and "nothing to do"
// should not need arithmetic on "0/0".
func TestBarJSONSaysNoUnreadInWords(t *testing.T) {
	per := []inbox.Count{{Address: "a@example.com"}}
	got := decode(t, capture(t, func() { printBarJSON(0, per, nil) }))
	if got.Text != "No unread" {
		t.Errorf("text = %q, want %q", got.Text, "No unread")
	}
	if got.Class != "" {
		t.Errorf("class = %q, want empty", got.Class)
	}
	// The icon is chosen from this, so an envelope stays shut on an empty inbox without it.
	if got.Alt != "read" {
		t.Errorf("alt = %q, want %q", got.Alt, "read")
	}
}

// TestBarJSONCountsSpamOnItsOwn: mail a filter got wrong is unread mail you have not seen, so a
// zero INBOX with junk waiting must not read as "No unread".
func TestBarJSONCountsSpamOnItsOwn(t *testing.T) {
	per := []inbox.Count{{Address: "a@example.com", Spam: 3}}
	got := decode(t, capture(t, func() { printBarJSON(3, per, nil) }))
	if got.Text != "0/3 Unread" {
		t.Errorf("text = %q, want %q", got.Text, "0/3 Unread")
	}
	if got.Alt != "unread" {
		t.Errorf("alt = %q, want %q", got.Alt, "unread")
	}
}

// TestBarJSONStillPrintsOnFailure is the one that matters for a status bar. A module whose command
// produces no output renders nothing, which on screen is indistinguishable from an inbox that is
// genuinely empty — so a broken registry has to be visible, not silent.
func TestBarJSONStillPrintsOnFailure(t *testing.T) {
	var code int
	out := capture(t, func() { code = printBarJSON(0, nil, os.ErrPermission) })
	got := decode(t, out)
	if got.Text != "!" {
		t.Errorf("text = %q, want %q", got.Text, "!")
	}
	if got.Class != "error" || got.Alt != "error" {
		t.Errorf("class %q / alt %q, want both %q", got.Class, got.Alt, "error")
	}
	if got.Tooltip == "" {
		t.Error("an error status with an empty tooltip says something is wrong but not what")
	}
	if code == 0 {
		t.Error("a failure should still return non-zero")
	}
}

// TestBarJSONWithNoAccountsSaysSo: an empty tooltip on a zero count leaves the reader unable to
// tell a quiet inbox from a mailforge that has never been set up.
func TestBarJSONWithNoAccountsSaysSo(t *testing.T) {
	got := decode(t, capture(t, func() { printBarJSON(0, nil, nil) }))
	if got.Tooltip != "no accounts" {
		t.Errorf("tooltip = %q, want %q", got.Tooltip, "no accounts")
	}
}
