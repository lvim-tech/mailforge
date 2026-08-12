package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/edit"
)

// scratch gives a config whose every path is inside a temp directory, so the whole write pipeline
// can run for real without a password store and without touching anything of the user's.
func scratch(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))

	c := config.Default()
	c.MaildirRoot = filepath.Join(dir, "mail")
	c.NeomuttDir = filepath.Join(dir, "config", "neomutt")
	c.MbsyncRC = filepath.Join(dir, "mbsyncrc")
	c.CertFile = "/etc/ssl/ca-bundle.pem"
	return c
}

func testAccount(addr string) account.Account {
	return account.Account{
		Address:  addr,
		RealName: "Test",
		IMAP:     account.Server{Host: "imap.example.com", Port: 993, Security: account.TLS},
		SMTP:     account.Server{Host: "smtp.example.com", Port: 465, Security: account.TLS},
		Folders:  account.Folders{Sent: "Sent", Drafts: "Drafts", Trash: "Trash"},
	}
}

// TestApplyWritesEverythingAnAccountNeeds walks the whole pipeline with no password — the one
// step that needs `pass` — and checks each artefact lands where the registry says it will.
func TestApplyWritesEverythingAnAccountNeeds(t *testing.T) {
	c := scratch(t)
	var reg account.Registry
	a := testAccount("hello@example.com")

	p := ApplyAccount(c, &reg, a, nil)
	if p.Failed() {
		t.Fatalf("apply failed:\n%s", strings.Join(Describe(p), "\n"))
	}

	for _, path := range []string{
		a.Maildir(c),
		a.RCFile(c),
		c.MbsyncRC,
		filepath.Join(c.NeomuttDir, "mailforge", "accounts.rc"),
		filepath.Join(c.NeomuttDir, "neomuttrc"),
		account.RegistryPath(),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing: %s", path)
		}
	}

	// The registry is the source of truth, so it must actually hold the account.
	got, err := account.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Find(a.Address); !ok {
		t.Errorf("the account is not in the registry")
	}
	// And the first account added becomes the default without being asked.
	if d, ok := got.Default(); !ok || d.Address != a.Address {
		t.Errorf("the only account is not the default")
	}
}

// TestApplyTwiceChangesNothing is what makes the program safe to run repeatedly: the second apply
// of an unchanged account must report every step as already up to date and rewrite no file.
func TestApplyTwiceChangesNothing(t *testing.T) {
	c := scratch(t)
	var reg account.Registry
	a := testAccount("a@example.com")

	if p := ApplyAccount(c, &reg, a, nil); p.Failed() {
		t.Fatalf("first apply failed:\n%s", strings.Join(Describe(p), "\n"))
	}
	before := mtimes(t, c, a)

	if p := ApplyAccount(c, &reg, a, nil); p.Failed() {
		t.Fatalf("second apply failed:\n%s", strings.Join(Describe(p), "\n"))
	}
	after := mtimes(t, c, a)

	for path, was := range before {
		if after[path] != was {
			t.Errorf("an identical apply rewrote %s", path)
		}
	}
}

func mtimes(t *testing.T, c config.Config, a account.Account) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, p := range []string{
		a.RCFile(c),
		c.MbsyncRC,
		filepath.Join(c.NeomuttDir, "mailforge", "accounts.rc"),
		filepath.Join(c.NeomuttDir, "neomuttrc"),
	} {
		if fi, err := os.Stat(p); err == nil {
			out[p] = fi.ModTime().UnixNano()
		}
	}
	return out
}

// TestRemoveLeavesMailAndPasswordAlone: deleting a record and deleting somebody's correspondence
// are different acts, and the default must be the narrow one.
func TestRemoveLeavesMailAndPasswordAlone(t *testing.T) {
	c := scratch(t)
	var reg account.Registry
	a := testAccount("a@example.com")
	if p := ApplyAccount(c, &reg, a, nil); p.Failed() {
		t.Fatal("apply failed")
	}
	// Something in the maildir, standing in for fetched mail.
	msg := filepath.Join(a.Maildir(c), "message")
	if err := os.WriteFile(msg, []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}

	if p := RemoveAccount(c, &reg, a, false, false); p.Failed() {
		t.Fatalf("remove failed:\n%s", strings.Join(Describe(p), "\n"))
	}

	if _, err := os.Stat(msg); err != nil {
		t.Errorf("the mail was deleted by a plain remove")
	}
	if _, err := os.Stat(a.RCFile(c)); err == nil {
		t.Errorf("the rc file survived the remove")
	}
	has, err := edit.Has(c.MbsyncRC, a.Address)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Errorf("the mbsync block survived the remove")
	}
	got, err := account.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Find(a.Address); ok {
		t.Errorf("the account is still in the registry")
	}
}

// TestTwoAccountsShareTheFileWithoutDisturbingEachOther is the drift case that started this whole
// program: one address's block must never touch another's.
func TestTwoAccountsShareTheFileWithoutDisturbingEachOther(t *testing.T) {
	c := scratch(t)
	var reg account.Registry
	a := testAccount("a@example.com")
	b := testAccount("b@example.com")

	for _, acc := range []account.Account{a, b} {
		if p := ApplyAccount(c, &reg, acc, nil); p.Failed() {
			t.Fatalf("apply of %s failed", acc.Address)
		}
	}
	if p := RemoveAccount(c, &reg, a, false, false); p.Failed() {
		t.Fatal("remove failed")
	}

	body, ok, err := edit.Body(c.MbsyncRC, b.Address)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("removing one account took the other's block with it")
	}
	if !strings.Contains(body, "Channel b@example.com") {
		t.Errorf("the surviving block is damaged:\n%s", body)
	}
	// And the index no longer mentions the account that went.
	idx, err := os.ReadFile(filepath.Join(c.NeomuttDir, "mailforge", "accounts.rc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(idx), "a@example.com") {
		t.Errorf("the index still lists the removed account:\n%s", idx)
	}
	if !strings.Contains(string(idx), "b@example.com") {
		t.Errorf("the index lost the account that stayed:\n%s", idx)
	}
}

// TestAuditSeesAHandEditedOwnedFile: the whole product is agreement between files, so the check
// that proves it has to actually catch a change.
func TestAuditSeesAHandEditedOwnedFile(t *testing.T) {
	c := scratch(t)
	var reg account.Registry
	a := testAccount("a@example.com")
	if p := ApplyAccount(c, &reg, a, nil); p.Failed() {
		t.Fatal("apply failed")
	}

	rep := Audit(c, reg)
	for _, f := range rep.Findings {
		if f.Level == Fail && !strings.Contains(f.Message, "password") {
			t.Errorf("a fresh apply is not clean: %s — %s", f.Subject, f.Message)
		}
	}

	// Somebody edits a generated file by hand.
	if err := os.WriteFile(a.RCFile(c), []byte("set realname = \"someone else\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep = Audit(c, reg)
	found := false
	for _, f := range rep.Findings {
		if strings.Contains(f.Message, "differs from what would be generated") {
			found = true
		}
	}
	if !found {
		t.Errorf("the audit did not notice a hand-edited owned file")
	}

	// And Fix puts it back.
	if p := Fix(c, &reg); p.Failed() {
		t.Fatalf("fix failed:\n%s", strings.Join(Describe(p), "\n"))
	}
	rep = Audit(c, reg)
	for _, f := range rep.Findings {
		if strings.Contains(f.Message, "differs from what would be generated") {
			t.Errorf("fix did not repair the drift it can repair")
		}
	}
}

// TestAuditSeesAnOrphanBlock: a block nothing claims keeps fetching mail for an account that is
// supposed to be gone.
func TestAuditSeesAnOrphanBlock(t *testing.T) {
	c := scratch(t)
	var reg account.Registry
	a := testAccount("a@example.com")
	if p := ApplyAccount(c, &reg, a, nil); p.Failed() {
		t.Fatal("apply failed")
	}
	// A block for an account the registry has never heard of.
	if _, err := edit.Upsert(c.MbsyncRC, "ghost@example.com", "Channel ghost@example.com\n", false); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, f := range Audit(c, reg).Findings {
		if strings.Contains(f.Message, "ghost@example.com") {
			found = true
		}
	}
	if !found {
		t.Errorf("an orphan block went unreported")
	}
}

// TestSeedsAreUsable: the audit reports a missing seed and [f] writes it back from this list, so an
// entry with no body would "fix" the problem by creating an empty file NeoMutt still cannot use.
func TestSeedsAreUsable(t *testing.T) {
	seeds := Seeds(config.Default())
	if len(seeds) == 0 {
		t.Fatal("no seeds at all")
	}
	names := map[string]bool{}
	for _, s := range seeds {
		if s.Name == "" || s.Body == "" || s.Why == "" {
			t.Errorf("incomplete seed: %+v", s)
		}
		if names[s.Name] {
			t.Errorf("%s is seeded twice", s.Name)
		}
		names[s.Name] = true
	}
	// mailcap is the one that went missing unnoticed, because it was added to the program after
	// this machine's first run and nothing walked the list afterwards.
	for _, want := range []string{"settings.rc", "colors.rc", "bindings.rc", "mailcap"} {
		if !names[want] {
			t.Errorf("%s is not among the seeds the doctor checks", want)
		}
	}
}

// TestFixWritesAMissingSeedAndLeavesAnExistingOne is the whole point of putting seeds behind [f]:
// it has to be able to create what is absent WITHOUT touching what the user has since edited.
func TestFixWritesAMissingSeedAndLeavesAnExistingOne(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.NeomuttDir = dir
	c.MbsyncRC = filepath.Join(dir, "mbsyncrc")

	mine := "# my own colours, edited by hand\n"
	if err := os.WriteFile(filepath.Join(dir, "colors.rc"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := &account.Registry{}
	if p := Fix(c, reg); p.Failed() {
		t.Fatalf("fix failed: %v", Describe(p))
	}

	if _, err := os.Stat(filepath.Join(dir, "mailcap")); err != nil {
		t.Errorf("a missing seed was not written: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "colors.rc"))
	if err != nil || string(got) != mine {
		t.Errorf("an existing seed was overwritten: %q", string(got))
	}
}

// TestSignatureBodyEndsInOneNewline: NeoMutt appends the file verbatim, so the blank lines an
// editor leaves behind become blank lines at the bottom of every message the account sends.
func TestSignatureBodyEndsInOneNewline(t *testing.T) {
	for _, in := range []string{"Bob", "Bob\n", "Bob\n\n\n", "Bob\n  \t\n"} {
		got := signatureBody(account.Account{Signature: in})
		if got != "Bob\n" {
			t.Errorf("%q became %q", in, got)
		}
	}
}

// TestApplyWritesAndRemovesTheSignature: an emptied signature must take the FILE with it. An empty
// file that still exists is a "-- " separator and a blank line on every message, which reads as a
// signature somebody forgot to finish.
func TestApplyWritesAndRemovesTheSignature(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.NeomuttDir = filepath.Join(dir, "neomutt")
	c.MaildirRoot = filepath.Join(dir, "mail")
	c.MbsyncRC = filepath.Join(dir, "mbsyncrc")

	a := account.Account{
		Address:   "bob@example.com",
		Signature: "Bob\nExample Ltd.",
		IMAP:      account.Server{Host: "imap.example.com", Port: 993, Security: account.TLS},
		SMTP:      account.Server{Host: "smtp.example.com", Port: 465, Security: account.TLS},
	}
	reg := &account.Registry{}
	if p := ApplyAccount(c, reg, a, nil); p.Failed() {
		t.Fatalf("apply failed: %v", Describe(p))
	}
	b, err := os.ReadFile(a.SigFile(c))
	if err != nil || string(b) != "Bob\nExample Ltd.\n" {
		t.Fatalf("signature file is %q, %v", string(b), err)
	}

	a.Signature = ""
	if p := ApplyAccount(c, reg, a, nil); p.Failed() {
		t.Fatalf("second apply failed: %v", Describe(p))
	}
	if _, err := os.Stat(a.SigFile(c)); !os.IsNotExist(err) {
		t.Errorf("the file outlived the signature: %v", err)
	}
	rc, _ := os.ReadFile(a.RCFile(c))
	if !strings.Contains(string(rc), "unset signature") {
		t.Error("the rc still points at a signature that is gone")
	}
}
