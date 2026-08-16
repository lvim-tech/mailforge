package render

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
)

// The two accounts every test uses: one ordinary hosted mailbox whose IMAP and SMTP live on
// different machines, and one Gmail, which is where every provider quirk shows up.
func fixtures() (config.Config, account.Account, account.Account) {
	c := config.Default()
	c.MaildirRoot = "/home/u/.local/share/mail"
	c.NeomuttDir = "/home/u/.config/neomutt"
	c.CertFile = "/etc/ssl/ca-bundle.pem"

	hosted := account.Account{
		Address:  "hello@example.com",
		RealName: "Motion Agency",
		IMAP:     account.Server{Host: "siera.superhosting.bg", Port: 993, Security: account.TLS},
		SMTP:     account.Server{Host: "credo.superhosting.bg", Port: 465, Security: account.TLS},
		Folders:  account.Folders{Sent: "Sent", Drafts: "Drafts", Trash: "Trash", Spam: "spam"},
		Default:  true,
	}
	gmail := account.Account{
		Address:  "someone@gmail.com",
		RealName: "Biser Stoilov",
		IMAP:     account.Server{Host: "imap.gmail.com", Port: 993, Security: account.TLS},
		SMTP:     account.Server{Host: "smtp.gmail.com", Port: 587, Security: account.STARTTLS},
		Folders:  account.Folders{Sent: "[Gmail]/Sent Mail", Drafts: "[Gmail]/Drafts", Trash: "[Gmail]/Trash"},
		Quirks:   account.Quirks{NoRecord: true, SyncExclude: []string{"[Gmail]/All Mail"}},
		Order:    1,
	}
	return c, hosted, gmail
}

// TestSMTPURLEncodesTheLogin is the single most likely mistake in a hand-written smtp_url: the
// login is an address, so it contains an @, and an unencoded one makes the host
// "example.com@credo.superhosting.bg" — a DNS failure naming a host nobody typed.
func TestSMTPURLEncodesTheLogin(t *testing.T) {
	_, hosted, gmail := fixtures()
	if got, want := hosted.SMTPURL(), "smtps://hello%40example.com@credo.superhosting.bg:465/"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// STARTTLS is a different scheme, not just a different port.
	if got := gmail.SMTPURL(); !strings.HasPrefix(got, "smtp://") {
		t.Errorf("starttls should not render as smtps: %s", got)
	}
}

func TestAccountRCHosted(t *testing.T) {
	c, hosted, _ := fixtures()
	got := AccountRC(c, hosted)
	for _, want := range []string{
		`set realname               = "Motion Agency"`,
		`set from                   = "hello@example.com"`,
		`set folder                 = "/home/u/.local/share/mail/hello@example.com"`,
		`set spoolfile              = "+INBOX"`,
		`set record                 = "+Sent"`,
		`set postponed              = "+Drafts"`,
		`set trash                  = "+Trash"`,
		`set smtp_url               = "smtps://hello%40example.com@credo.superhosting.bg:465/"`,
		// Single-quoted: the entry is built from user input and lands in a shell command line.
		"pass show 'mail/hello@example.com' | head -1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line:\n  %s\n--- rendered ---\n%s", want, got)
		}
	}
	// The password itself must never be in a rendered file.
	if strings.Contains(strings.ToLower(got), "password") {
		t.Errorf("the word password appears in a rendered rc; nothing there should carry one:\n%s", got)
	}
}

// TestGmailDoesNotFileSentMailTwice: Gmail saves what you send by itself. The old hand-made config
// set record to [Gmail]/Sent Mail as well, so every sent message existed twice.
func TestGmailDoesNotFileSentMailTwice(t *testing.T) {
	c, _, gmail := fixtures()
	got := AccountRC(c, gmail)
	// "\nset record", not "set record" — the latter is a substring of "unset record", which is
	// exactly what this test wants to see. A containment check that matches its own success
	// condition is a test that can only fail.
	if strings.Contains(got, "\nset record") {
		t.Errorf("record is set for an account whose server files sent mail:\n%s", got)
	}
	if !strings.Contains(got, "unset record") {
		t.Errorf("record should be explicitly unset, not merely absent:\n%s", got)
	}
	if !strings.Contains(got, `set postponed              = "+[Gmail]/Drafts"`) {
		t.Errorf("the discovered folder name did not reach the file:\n%s", got)
	}
}

// TestMbsyncBlockUsesTheCurrentKeyword: isync 1.5 renamed SSLType to TLSType. The old spelling
// prints a deprecation notice today and stops working later — measured on this machine.
func TestMbsyncBlockUsesTheCurrentKeyword(t *testing.T) {
	c, hosted, _ := fixtures()
	got := MbsyncBlock(c, hosted)
	if strings.Contains(got, "SSLType") {
		t.Errorf("the deprecated keyword is still being written:\n%s", got)
	}
	if !strings.Contains(got, "TLSType IMAPS") {
		t.Errorf("TLSType missing:\n%s", got)
	}
	// AuthMechs pinned to LOGIN disables every better mechanism the server offers.
	if strings.Contains(got, "AuthMechs") {
		t.Errorf("AuthMechs should be left at the default:\n%s", got)
	}
	for _, want := range []string{
		"IMAPStore hello@example.com-remote",
		"Host siera.superhosting.bg",
		"Port 993",
		"User hello@example.com",
		`PassCmd "pass show 'mail/hello@example.com' | head -1"`,
		"CertificateFile /etc/ssl/ca-bundle.pem",
		"MaildirStore hello@example.com-local",
		"Path /home/u/.local/share/mail/hello@example.com/",
		"Inbox /home/u/.local/share/mail/hello@example.com/INBOX",
		"Channel hello@example.com",
		"Far :hello@example.com-remote:",
		"Near :hello@example.com-local:",
		"Patterns *",
		"Create Both",
		"Expunge Both",
		"SyncState *",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line:\n  %s\n--- rendered ---\n%s", want, got)
		}
	}
}

// TestGmailExcludesAllMail: All Mail is every message a second time; fetching it doubles the
// maildir for no benefit.
func TestGmailExcludesAllMail(t *testing.T) {
	c, _, gmail := fixtures()
	got := MbsyncBlock(c, gmail)
	if !strings.Contains(got, `Patterns * !"[Gmail]/All Mail"`) {
		t.Errorf("All Mail is not excluded:\n%s", got)
	}
	if !strings.Contains(got, "TLSType STARTTLS") {
		// The Gmail fixture uses STARTTLS on the SMTP side only; its IMAP is TLS. This
		// guards the mapping rather than the fixture.
		if !strings.Contains(got, "TLSType IMAPS") {
			t.Errorf("neither security mapped:\n%s", got)
		}
	}
}

func TestAccountsRCOrdersAndSourcesTheDefaultLast(t *testing.T) {
	c, hosted, gmail := fixtures()
	got := AccountsRC(c, []account.Account{gmail, hosted}) // deliberately out of order

	iHosted := strings.Index(got, "named-mailboxes \"EXAMPLE.COM | HELLO\"")
	iGmail := strings.Index(got, "named-mailboxes \"GMAIL.COM | SOMEONE\"")
	if iHosted < 0 || iGmail < 0 {
		t.Fatalf("a sidebar header is missing:\n%s", got)
	}
	if iHosted > iGmail {
		t.Errorf("Order was not honoured — the list must not depend on the slice order")
	}
	if !strings.Contains(got, `folder-hook "hello@example.com/*" source /home/u/.config/neomutt/mailforge/accounts/hello@example.com.rc`) {
		t.Errorf("folder-hook missing or wrong:\n%s", got)
	}
	// The default is sourced last, so its identity is the one in force at startup.
	last := strings.LastIndex(got, "source ")
	if !strings.Contains(got[last:], "hello@example.com.rc") {
		t.Errorf("the default account is not sourced last:\n%s", got)
	}
}

// TestQuoteEscapes: a display name carrying a quote would otherwise end the string early and take
// the rest of the file's parsing with it.
func TestQuoteEscapes(t *testing.T) {
	c, hosted, _ := fixtures()
	hosted.RealName = `Bob "The Builder" \ Co`
	got := AccountRC(c, hosted)
	if !strings.Contains(got, `set realname               = "Bob \"The Builder\" \\ Co"`) {
		t.Errorf("quoting is wrong:\n%s", got)
	}
}

// TestEveryOwnedFileSaysSo: a generated file with no header is a file somebody will edit.
func TestEveryOwnedFileSaysSo(t *testing.T) {
	c, hosted, _ := fixtures()
	for name, got := range map[string]string{
		"account rc":  AccountRC(c, hosted),
		"accounts rc": AccountsRC(c, []account.Account{hosted}),
	} {
		if !strings.HasPrefix(got, Header) {
			t.Errorf("%s does not carry the generated header", name)
		}
	}
}

// TestAccountRCSignature: the rc names the FILE rather than carrying the text, so editing the
// signature takes effect on the next message instead of on the next apply.
func TestAccountRCSignature(t *testing.T) {
	c := config.Default()
	a := account.Account{
		Address: "bob@example.com",
		IMAP:    account.Server{Host: "imap.example.com", Port: 993, Security: account.TLS},
		SMTP:    account.Server{Host: "smtp.example.com", Port: 465, Security: account.TLS},
	}

	// With none, the rc must say so out loud. The account files are sourced by a folder-hook and
	// nothing resets a variable between them, so a missing `unset` signs this account with
	// whatever the last one set.
	if got := AccountRC(c, a); !strings.Contains(got, "unset signature") {
		t.Errorf("an account with no signature does not unset it:\n%s", got)
	}

	a.Signature = "Bob\nExample Ltd."
	got := AccountRC(c, a)
	if !strings.Contains(got, `set signature `) || !strings.Contains(got, a.SigFile(c)) {
		t.Errorf("the signature file is not named:\n%s", got)
	}
	if strings.Contains(got, "Example Ltd.") {
		t.Error("the signature text was inlined into the rc instead of being left in the file")
	}
}

// TestOptionsRC: the editor is written as a PATH. The seeded `set editor = "$EDITOR"` it replaces
// was expanded once at parse time, out of whatever environment started NeoMutt — empty from a
// launcher that does not export it, and composing then failed naming the variable.
func TestOptionsRC(t *testing.T) {
	c := config.Default()
	c.Editor = "nvim"
	got := OptionsRC(c)
	if !strings.Contains(got, `set editor = "nvim"`) {
		t.Errorf("the editor is not set:\n%s", got)
	}
	if strings.Contains(got, "$EDITOR") && !strings.Contains(got, "# ") {
		t.Error("the rendered file still leans on the environment")
	}

	// Empty is a deliberate "no opinion", and must not render an empty setting — `set editor =
	// ""` is an editor NeoMutt would try to run.
	c.Editor = "  "
	got = OptionsRC(c)
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "set editor") {
			t.Errorf("an empty editor was written as a setting: %q", l)
		}
	}
}

// TestParseEveryAndHuman: the interval is stored as one number of seconds and shown in whatever
// unit reads best, so the two have to be inverses for the forms a person actually types.
func TestParseEveryAndHuman(t *testing.T) {
	cases := map[string]int{
		"30s": 30, "30": 30, "90": 90,
		"5m": 300, "5min": 300, "1h": 3600, "2h": 7200,
		" 45s ": 45, "0": 0,
	}
	for in, want := range cases {
		got, err := ParseEvery(in)
		if err != nil || got != want {
			t.Errorf("ParseEvery(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "-5", "5x", "m"} {
		if _, err := ParseEvery(bad); err == nil {
			t.Errorf("ParseEvery(%q) was accepted", bad)
		}
	}
	for sec, want := range map[int]string{0: "never", 30: "30 s", 90: "90 s", 300: "5 min", 3600: "1 h"} {
		if got := HumanEvery(sec); got != want {
			t.Errorf("HumanEvery(%d) = %q, want %q", sec, got, want)
		}
	}
}

// TestTimerTightensAccuracyOnlyWhenItMatters: systemd's default AccuracySec is a whole minute,
// spent batching wakeups. Under it a 30-second timer fires about once a minute — accepted,
// written, shown on screen and quietly not honoured.
func TestTimerTightensAccuracyOnlyWhenItMatters(t *testing.T) {
	c := config.Default()

	c.SyncEvery = 30
	if got := MbsyncTimer(c); !strings.Contains(got, "AccuracySec=1s") ||
		!strings.Contains(got, "OnUnitActiveSec=30s") {
		t.Errorf("a sub-minute interval was not given the accuracy to keep it:\n%s", got)
	}

	c.SyncEvery = 300
	if got := MbsyncTimer(c); !strings.Contains(got, "AccuracySec=1min") {
		t.Errorf("a five-minute fetch should batch like everything else:\n%s", got)
	}
}
