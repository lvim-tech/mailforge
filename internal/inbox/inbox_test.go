package inbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
)

// TestUnreadCountsBothHalvesOfTheMaildir is the bug the script it replaces had.
//
// It counted new/ alone, so every message a client had looked at without opening — moved to cur/
// with no S flag — reported as read. A bar saying zero while unread mail sits there is worse than
// no bar: it is the same thing it shows when everything really has been read.
func TestUnreadCountsBothHalvesOfTheMaildir(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.MaildirRoot = dir

	a := account.Account{
		Address: "bob@example.com",
		Folders: account.Folders{Delim: ".", Spam: "INBOX.spam"},
	}
	box := filepath.Join(dir, a.Address)

	write := func(folder, sub, name string) {
		t.Helper()
		p := filepath.Join(box, folder, sub)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("INBOX", "new", "1.a.host")          // unread by definition
	write("INBOX", "cur", "2.a.host,U=2:2,")   // in cur, no S: unread
	write("INBOX", "cur", "3.a.host,U=3:2,S")  // seen
	write("INBOX", "cur", "4.a.host,U=4:2,FS") // seen and flagged
	write("spam", "new", "5.a.host")           // the spam folder, LOCAL name
	write("Trash", "new", "6.a.host")          // not counted: read or not, it is gone
	write("INBOX", "cur", ".hidden")           // dotfiles are maildir bookkeeping

	total, per := Unread(c, []account.Account{a})
	if total != 3 {
		t.Errorf("total = %d, want 3 (one new, one unseen in cur, one in spam)", total)
	}
	if len(per) != 1 || per[0].Unread() != 3 || per[0].Address != a.Address {
		t.Errorf("per-account = %+v", per)
	}
	// And split the way a status bar shows them: two waiting in INBOX, one a filter decided for
	// you. Summed, those two facts are indistinguishable.
	if per[0].Inbox != 2 || per[0].Spam != 1 {
		t.Errorf("split = inbox %d, spam %d; want 2 and 1", per[0].Inbox, per[0].Spam)
	}
	if in, spam := Split(per); in != 2 || spam != 1 {
		t.Errorf("Split = %d, %d; want 2, 1", in, spam)
	}
}

// TestUnreadOnAMachineWithNothing: a missing maildir is zero, not a failure. An account added
// before its first fetch has no directories at all, and a bar must not go red for that.
func TestUnreadOnAMachineWithNothing(t *testing.T) {
	c := config.Default()
	c.MaildirRoot = t.TempDir()
	total, per := Unread(c, []account.Account{{Address: "nobody@example.com"}})
	if total != 0 || len(per) != 1 {
		t.Errorf("total = %d, per = %+v", total, per)
	}
}
