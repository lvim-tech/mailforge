package render

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
)

// NeoMutt substitutes backticks inside double-quoted values — this package depends on that for
// smtp_pass — so quote() output was never inert. A real name of "Joe `id`" rendered as a command
// that ran on every NeoMutt start, and a newline in one ended the string and turned the rest of the
// value into configuration.
func TestQuoteNeutralisesCommandSubstitutionAndLineBreaks(t *testing.T) {
	got := quote("Joe `touch /tmp/pwn`")
	for i, r := range got {
		if r == '`' && (i == 0 || got[i-1] != '\\') {
			t.Errorf("an unescaped backtick survived quoting: %s", got)
			break
		}
	}
	if got := quote("Joe\nset my_pwn = 1"); strings.ContainsAny(got, "\r\n") {
		t.Errorf("a line break survived quoting: %q", got)
	}
	// And the ordinary cases still round-trip readably.
	if got, want := quote(`say "hi"`), `"say \"hi\""`; got != want {
		t.Errorf("quote(%q) = %s, want %s", `say "hi"`, got, want)
	}
}

func TestShellQuoteContainsTheValue(t *testing.T) {
	if got, want := shellQuote("mail/someone@example.com"), `'mail/someone@example.com'`; got != want {
		t.Errorf("shellQuote = %s, want %s", got, want)
	}
	// The one character single quoting cannot contain by itself.
	if got, want := shellQuote("it's"), `'it'\''s'`; got != want {
		t.Errorf("shellQuote = %s, want %s", got, want)
	}
}

// The pass entry is built from the address and the configured prefix and lands inside a command
// line that NeoMutt and mbsync hand to /bin/sh. Both call sites must quote it.
func TestPassEntryIsShellQuotedEverywhereItIsRun(t *testing.T) {
	c := config.Default()
	c.PassPrefix = "mail"
	a := account.Account{
		Address: "someone@example.com",
		IMAP:    account.Server{Host: "imap.example.com", Port: 993, Security: account.TLS},
		SMTP:    account.Server{Host: "smtp.example.com", Port: 465, Security: account.TLS},
	}
	want := "pass show 'mail/someone@example.com' | head -1"
	if got := AccountRC(c, a); !strings.Contains(got, want) {
		t.Errorf("neomuttrc does not quote the entry:\n%s", got)
	}
	if got := MbsyncBlock(c, a); !strings.Contains(got, want) {
		t.Errorf("mbsync block does not quote the entry:\n%s", got)
	}
}
