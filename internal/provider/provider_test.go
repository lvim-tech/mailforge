package provider

import (
	"net"
	"testing"

	"github.com/lvim-tech/mailforge/internal/account"
)

func TestTableCarriesTheQuirksNotJustTheHosts(t *testing.T) {
	g, ok := fromTable("gmail.com")
	if !ok {
		t.Fatal("gmail is not in the table")
	}
	if g.IMAP.Host != "imap.gmail.com" || g.IMAP.Port != 993 || g.IMAP.Security != account.TLS {
		t.Errorf("imap: %+v", g.IMAP)
	}
	// The quirks are the reason the table exists at all — hosts alone would be a directory.
	if !g.Quirks.NoRecord {
		t.Errorf("gmail files sent mail itself; the quirk is missing")
	}
	if len(g.Quirks.SyncExclude) == 0 {
		t.Errorf("All Mail is not excluded; every message would be fetched twice")
	}
	if g.Note == "" {
		t.Errorf("the app-password note is what stops a support question")
	}
	if _, ok := fromTable("example.com"); ok {
		t.Errorf("a shared-hosting domain must not be in the table: its server name is not " +
			"derivable from the address")
	}
}

func TestDomainOf(t *testing.T) {
	for in, want := range map[string]string{
		"hello@example.com": "example.com",
		"a@B.CO":            "b.co",
		"no-at-sign":        "",
		"":                  "",
	} {
		if got := domainOf(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// TestExpandPlaceholders: the ISPDB writes the login as a template, and a login stored as the
// literal "%EMAILADDRESS%" fails at authentication with a message about the password.
func TestExpandPlaceholders(t *testing.T) {
	for _, tc := range []struct{ in, addr, want string }{
		{"%EMAILADDRESS%", "a@b.com", ""}, // equals the address: the default, not worth storing
		{"%EMAILLOCALPART%", "a@b.com", "a"},
		{"", "a@b.com", ""},
		{"fixed", "a@b.com", "fixed"},
	} {
		if got := expand(tc.in, tc.addr); got != tc.want {
			t.Errorf("expand(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPickServerPrefersTLS: a database listing both is expressing a preference, and the encrypted
// one is it.
func TestPickServerPrefersTLS(t *testing.T) {
	list := []ispdbServer{
		{Type: "imap", Hostname: "plain.example", Port: 143, SocketType: "STARTTLS"},
		{Type: "imap", Hostname: "tls.example", Port: 993, SocketType: "SSL"},
	}
	got, ok := pickServer(list, "imap")
	if !ok || got.Hostname != "tls.example" {
		t.Errorf("picked %+v", got)
	}
	// With only STARTTLS on offer, take it rather than nothing.
	got, ok = pickServer(list[:1], "imap")
	if !ok || got.Hostname != "plain.example" {
		t.Errorf("picked %+v", got)
	}
	if _, ok := pickServer(list, "pop3"); ok {
		t.Errorf("a type nobody asked for was returned")
	}
}

func TestSecurityMapping(t *testing.T) {
	if security("STARTTLS") != account.STARTTLS {
		t.Errorf("starttls not mapped")
	}
	if security("SSL") != account.TLS || security("") != account.TLS {
		t.Errorf("anything not starttls should be plain TLS")
	}
}

// TestNoDomainIsNotACrash: the wizard can reach discovery with a half-typed address.
func TestNoDomainIsNotACrash(t *testing.T) {
	g := Discover(nil, "not-an-address", false) //nolint:staticcheck // nil ctx is the point
	if g.Found() {
		t.Errorf("something was discovered for a non-address: %+v", g)
	}
	if g.Source == "" {
		t.Errorf("a failure with no explanation is a spinner that stopped for no reason")
	}
}

// TestMXHosts covers the ordering and the two skips, which is all of fromMX that does not need a
// network: preference order decides which host is offered, and every candidate that is not skipped
// costs a full dial timeout when it is dead.
func TestMXHosts(t *testing.T) {
	recs := []*net.MX{
		{Host: "backup.example.net.", Pref: 20},
		{Host: "eu108.jethosting.com.", Pref: 10},
		{Host: "EU108.JETHOSTING.COM.", Pref: 30}, // the same host again, differently cased
		{Host: "example.org.", Pref: 40},          // the domain itself: already probed
		{Host: ".", Pref: 50},
	}
	got := mxHosts(recs, "example.org")
	want := []string{"eu108.jethosting.com", "backup.example.net"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
