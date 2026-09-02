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

// TestBarJSONMarksUnread: the class is the whole reason a bar can look different when mail is
// waiting, and an always-present one makes the styling rule it feeds meaningless.
func TestBarJSONMarksUnread(t *testing.T) {
	per := []inbox.Count{{Address: "a@example.com", Unread: 2}, {Address: "b@example.com", Unread: 0}}
	got := decode(t, capture(t, func() { printBarJSON(2, per, nil) }))
	if got.Text != "2" {
		t.Errorf("text = %q, want %q", got.Text, "2")
	}
	if got.Class != "unread" {
		t.Errorf("class = %q, want %q", got.Class, "unread")
	}
	for _, want := range []string{"a@example.com", "b@example.com"} {
		if !strings.Contains(got.Tooltip, want) {
			t.Errorf("tooltip is missing %q: %q", want, got.Tooltip)
		}
	}
}

// TestBarJSONWithNothingToReadHasNoClass — the mirror of the test above.
func TestBarJSONWithNothingToReadHasNoClass(t *testing.T) {
	per := []inbox.Count{{Address: "a@example.com", Unread: 0}}
	got := decode(t, capture(t, func() { printBarJSON(0, per, nil) }))
	if got.Text != "0" || got.Class != "" {
		t.Errorf("got %+v, want text 0 and no class", got)
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
	if got.Class != "error" {
		t.Errorf("class = %q, want %q", got.Class, "error")
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
