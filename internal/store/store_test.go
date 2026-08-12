package store

import (
	"io"
	"strings"
	"testing"
)

func body(t *testing.T, secret, trailer string) string {
	t.Helper()
	var b strings.Builder
	err := writeBody(&b, func(w io.Writer) error {
		_, err := io.WriteString(w, secret)
		return err
	}, trailer)
	if err != nil {
		t.Fatalf("writeBody: %v", err)
	}
	return b.String()
}

// TestWriteBodyPutsThePasswordFirst: `pass show -c` copies line one and nothing else, so anything
// written above the password would be copied INSTEAD of it — and the user would paste a URL into a
// login form and think the store was broken.
func TestWriteBodyPutsThePasswordFirst(t *testing.T) {
	got := body(t, "hunter2", "login: bob\n")
	if !strings.HasPrefix(got, "hunter2\n") {
		t.Errorf("the password is not the first line:\n%q", got)
	}
}

// TestWriteBodyKeepsTheTrailerVerBatim is the whole reason the parameter is a string.
//
// An entry's lower half is not necessarily a set of fields: a free-form note has no colon in it,
// and an otpauth line is a URL whose colon is part of the scheme. A writer that rendered a map
// would drop the first and mangle the second.
func TestWriteBodyKeepsTheTrailerVerbatim(t *testing.T) {
	trailer := "login: bob\n" +
		"otpauth://totp/Example:bob?secret=ABC&issuer=Example\n" +
		"a note with no colon at all\n" +
		"\n" +
		"  indented continuation\n"
	got := body(t, "hunter2", trailer)
	if got != "hunter2\n"+trailer {
		t.Errorf("the trailer was not preserved:\nwant %q\ngot  %q", "hunter2\n"+trailer, got)
	}
}

// TestWriteBodyTerminatesTheTrailer: without the final newline the last field runs into whatever a
// later writer appends, silently merging two lines into one.
func TestWriteBodyTerminatesTheTrailer(t *testing.T) {
	if got := body(t, "hunter2", "login: bob"); got != "hunter2\nlogin: bob\n" {
		t.Errorf("got %q", got)
	}
}

// TestWriteBodyWithNoTrailerWritesOnlyThePassword: an entry that never had a lower half must not
// gain a blank line on every rewrite, or a git-backed store shows a diff for a write that changed
// nothing.
func TestWriteBodyWithNoTrailerWritesOnlyThePassword(t *testing.T) {
	if got := body(t, "hunter2", ""); got != "hunter2\n" {
		t.Errorf("got %q", got)
	}
}

func TestRenderMetaIsSortedAndSkipsEmpty(t *testing.T) {
	got := renderMeta(map[string]string{"url": "https://x", "login": "bob", "note": "  "})
	if got != "login: bob\nurl: https://x\n" {
		t.Errorf("got %q", got)
	}
	// A newline in a value would forge an extra field.
	if got := renderMeta(map[string]string{"note": "one\ntwo"}); got != "note: one two\n" {
		t.Errorf("a value's newline survived: %q", got)
	}
	if got := renderMeta(nil); got != "" {
		t.Errorf("nil meta produced %q", got)
	}
}

// TestValidNameRejectsAnEscape: the name becomes a path under the store, and `pass` will encrypt
// over whatever it lands on.
func TestValidNameRejectsAnEscape(t *testing.T) {
	for _, bad := range []string{"", "  ", "/mail/x", "../../.ssh/id_ed25519", "mail//x", "a/./b"} {
		if err := ValidName(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, ok := range []string{"mail/bob@example.com", "websites/abv.bg/bob"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q was rejected: %v", ok, err)
		}
	}
}
