package account

import (
	"testing"

	"github.com/lvim-tech/mailforge/internal/config"
)

func acc(addr string) Account {
	return Account{
		Address: addr,
		IMAP:    Server{Host: "imap.example.com", Port: 993, Security: TLS},
		SMTP:    Server{Host: "smtp.example.com", Port: 465, Security: TLS},
	}
}

func TestDerivedNames(t *testing.T) {
	c := config.Default()
	c.MaildirRoot = "/m"
	c.NeomuttDir = "/n"
	c.PassPrefix = "mail"
	a := acc("hello@example.com")

	for _, tc := range []struct{ what, got, want string }{
		{"pass entry", a.PassEntry(c), "mail/hello@example.com"},
		{"maildir", a.Maildir(c), "/m/hello@example.com"},
		{"rc file", a.RCFile(c), "/n/mailforge/accounts/hello@example.com.rc"},
		{"channel", a.Channel(), "hello@example.com"},
		{"far store", a.FarStore(), "hello@example.com-remote"},
		{"near store", a.NearStore(), "hello@example.com-local"},
		{"label", a.DefaultLabel(), "EXAMPLE.COM | HELLO"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.what, tc.got, tc.want)
		}
	}
}

// TestValidateRefusesWhatCannotWork: the address becomes a filename, a maildir name and a pass
// entry, so a separator in it writes somewhere nobody asked for.
func TestValidateRefusesWhatCannotWork(t *testing.T) {
	for _, bad := range []Account{
		{},
		acc("not-an-address"),
		acc("a/b@example.com"),
		acc(".hidden@example.com"),
		{Address: "a@b.c", IMAP: Server{Host: "", Port: 993, Security: TLS}, SMTP: Server{Host: "s", Port: 1, Security: TLS}},
		{Address: "a@b.c", IMAP: Server{Host: "h", Port: 0, Security: TLS}, SMTP: Server{Host: "s", Port: 1, Security: TLS}},
		{Address: "a@b.c", IMAP: Server{Host: "h", Port: 993, Security: "plain"}, SMTP: Server{Host: "s", Port: 1, Security: TLS}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted an account that cannot work: %+v", bad)
		}
	}
	if err := acc("ok@example.com").Validate(); err != nil {
		t.Errorf("refused a good account: %v", err)
	}
}

// TestExactlyOneDefault: two accounts claiming the startup identity is a file that behaves
// differently depending on which line NeoMutt read last. accounts.json is hand-editable, so this
// is enforced on load as well as save.
func TestExactlyOneDefault(t *testing.T) {
	r := Registry{Accounts: []Account{acc("a@x.com"), acc("b@x.com"), acc("c@x.com")}}
	r.Accounts[0].Default = true
	r.Accounts[2].Default = true
	r.normalise()

	n := 0
	for _, a := range r.Accounts {
		if a.Default {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d accounts claim to be the default", n)
	}
	if !r.Accounts[0].Default {
		t.Errorf("the first claim should win, not the last")
	}

	// And an empty registry that gains one account gets a default without being asked.
	var empty Registry
	if err := empty.Upsert(acc("only@x.com")); err != nil {
		t.Fatal(err)
	}
	if d, ok := empty.Default(); !ok || d.Address != "only@x.com" {
		t.Errorf("the only account is not the default")
	}
}

func TestUpsertReplacesAndKeepsPosition(t *testing.T) {
	var r Registry
	for _, a := range []string{"a@x.com", "b@x.com", "c@x.com"} {
		if err := r.Upsert(acc(a)); err != nil {
			t.Fatal(err)
		}
	}
	updated := acc("b@x.com")
	updated.RealName = "Renamed"
	if err := r.Upsert(updated); err != nil {
		t.Fatal(err)
	}
	if len(r.Accounts) != 3 {
		t.Fatalf("upsert added a duplicate: %d accounts", len(r.Accounts))
	}
	if r.Accounts[1].Address != "b@x.com" || r.Accounts[1].RealName != "Renamed" {
		t.Errorf("the replacement did not keep its place: %+v", r.Accounts)
	}
}

func TestMoveAndSetDefault(t *testing.T) {
	var r Registry
	for _, a := range []string{"a@x.com", "b@x.com", "c@x.com"} {
		_ = r.Upsert(acc(a))
	}
	if !r.Move("c@x.com", -1) {
		t.Fatal("move failed")
	}
	if r.Accounts[1].Address != "c@x.com" {
		t.Errorf("order after move: %v", addresses(r))
	}
	if r.Move("a@x.com", -1) {
		t.Errorf("moving the first one up should not be possible")
	}
	if !r.SetDefault("b@x.com") {
		t.Fatal("set default failed")
	}
	if d, _ := r.Default(); d.Address != "b@x.com" {
		t.Errorf("default is %s", d.Address)
	}
}

// TestRemoveDoesNotTouchAnythingElse: removing a record and deleting somebody's mail are
// different acts and must not share a code path.
func TestRemoveDoesNotTouchAnythingElse(t *testing.T) {
	var r Registry
	_ = r.Upsert(acc("a@x.com"))
	_ = r.Upsert(acc("b@x.com"))
	if !r.Remove("a@x.com") {
		t.Fatal("remove reported failure")
	}
	if len(r.Accounts) != 1 || r.Accounts[0].Address != "b@x.com" {
		t.Errorf("wrong account removed: %v", addresses(r))
	}
	if !r.Accounts[0].Default {
		t.Errorf("removing the default left the registry with none")
	}
	if r.Remove("nobody@x.com") {
		t.Errorf("removing an absent account reported success")
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var r Registry
	a := acc("hello@example.com")
	a.RealName = "Motion Agency"
	a.Folders = Folders{Sent: "Sent"}
	a.Quirks = Quirks{SyncExclude: []string{"[Gmail]/All Mail"}}
	if err := r.Upsert(a); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("%d accounts came back", len(got.Accounts))
	}
	if got.Accounts[0].RealName != "Motion Agency" || got.Accounts[0].Folders.Sent != "Sent" {
		t.Errorf("what came back is not what went in: %+v", got.Accounts[0])
	}
}

func TestAbsentRegistryIsEmptyNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	r, err := Load()
	if err != nil {
		t.Fatalf("a machine before its first account is not an error: %v", err)
	}
	if len(r.Accounts) != 0 {
		t.Errorf("%d accounts out of nowhere", len(r.Accounts))
	}
}

func addresses(r Registry) []string {
	out := make([]string, 0, len(r.Accounts))
	for _, a := range r.Accounts {
		out = append(out, a.Address)
	}
	return out
}

// TestLocalFolderIsNotTheServerName is the distinction that cost a broken first sync.
//
// mbsync stores the mailbox the server calls "INBOX.Sent" in a directory called "Sent" — it drops
// the INBOX namespace and turns the delimiter into a slash. Writing the server's spelling into
// NeoMutt pointed it at nothing, and pre-creating directories under that spelling made mbsync
// refuse them: "mailbox name INBOX.Sent contains server's hierarchy delimiter".
func TestLocalFolderIsNotTheServerName(t *testing.T) {
	cpanel := acc("a@x.com")
	cpanel.Folders = Folders{
		Delim: ".", Sent: "INBOX.Sent", Drafts: "INBOX.Drafts", Trash: "INBOX.Trash",
		Archive: "INBOX.Archive", Spam: "INBOX.spam",
	}
	got := cpanel.LocalFolders()
	for _, tc := range []struct{ what, got, want string }{
		{"sent", got.Sent, "Sent"},
		{"drafts", got.Drafts, "Drafts"},
		{"trash", got.Trash, "Trash"},
		{"archive", got.Archive, "Archive"},
		{"spam", got.Spam, "spam"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.what, tc.got, tc.want)
		}
	}

	// Gmail's delimiter is already a slash and its names carry no INBOX prefix, so they pass
	// through untouched — the rule must not mangle what is already right.
	gmail := acc("b@gmail.com")
	gmail.Folders = Folders{Delim: "/", Sent: "[Gmail]/Sent Mail", Drafts: "[Gmail]/Drafts"}
	if l := gmail.LocalFolders(); l.Sent != "[Gmail]/Sent Mail" || l.Drafts != "[Gmail]/Drafts" {
		t.Errorf("gmail names were altered: %+v", l)
	}

	// A server that reported no delimiter leaves the names alone rather than guessing.
	none := acc("c@x.com")
	none.Folders = Folders{Sent: "Sent"}
	if none.LocalFolders().Sent != "Sent" {
		t.Errorf("a name was changed with no delimiter to go on")
	}
}
