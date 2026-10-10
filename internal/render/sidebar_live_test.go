package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/account"
)

/*
TestTheFoldedFilesLoadInTheRealNeoMutt hands the generated files to the client itself.

Worth the trouble because the mistakes this feature can make are mistakes a Go test cannot see. A
key name NeoMutt does not know is accepted as a literal sequence and never pressed; a function name
it does not have is an error banner on every start; and `named-mailboxes` pointed at a maildir that
is not there is refused outright — "Unknown Mailbox", at parse time, which is how a sidebar ends up
empty with the file looking perfectly reasonable.

Skipped when neomutt is not installed, so the suite still runs on a machine that only builds.
*/
func TestTheFoldedFilesLoadInTheRealNeoMutt(t *testing.T) {
	bin, err := exec.LookPath("neomutt")
	if err != nil {
		t.Skip("neomutt is not installed")
	}

	c, hosted, gmail := fixtures()
	c.SidebarCollapse = true
	c.MaildirRoot = t.TempDir()
	c.NeomuttDir = t.TempDir()

	accounts := []account.Account{hosted, gmail}
	// One account open, one shut: the two states have different lines, and a file that loads in
	// one of them is not a file that loads.
	body := SidebarRC(c, accounts, map[string]bool{hosted.Address: true}, gmail.Address)

	// The maildirs have to exist before NeoMutt is asked to register them. Taken from the
	// rendered file rather than rebuilt here, so the test cannot pass by creating directories
	// the file does not name.
	for _, dir := range quotedPaths(body) {
		for _, sub := range []string{"cur", "new", "tmp"} {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}

	// sidebar.rc and keys.rc only, and accounts.rc deliberately NOT. Its lines source the account
	// files, and those carry a `pass show` backtick and the sidebar's own — handing them to
	// NeoMutt here would run two programs against this machine's real password store and real
	// state file to prove something about a text file. What accounts.rc says is unit-tested; the
	// two files below are the ones whose every line is new.
	for name, text := range map[string]string{
		"sidebar.rc": body,
		"keys.rc":    KeysRC(c),
	} {
		p := filepath.Join(c.NeomuttDir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(bin, "-n", "-F", p, "-Q", "sidebar_width").CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if strings.Contains(string(out), "errors in") {
			t.Errorf("NeoMutt refused part of %s:\n%s", name, out)
		}
	}
}

var quoted = regexp.MustCompile(`"(/[^"]+)"`)

// quotedPaths pulls the mailbox paths out of a rendered file.
func quotedPaths(body string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}
