// Package inbox counts unread mail on disk.
//
// It exists so that a status bar, a launcher and an editor widget can ask ONE question of the same
// source the rest of this program uses. The script they shared before carried its own list of
// addresses and its own idea of where mail lived; when the maildir root moved, four things went
// quietly wrong at once and each had to be found separately. Derived from the registry, there is
// nothing to keep in step.
package inbox

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
)

// Count is one account's unread mail, split by where it landed.
//
// SPLIT, because the two numbers mean different things to the person reading them. Unread in INBOX
// is mail waiting; unread in spam is mail a filter decided for you, and the only reason to show it
// is so a wrong decision is visible. Summed into one figure, a bar reading "3" cannot say whether
// that is three people waiting for an answer or three pieces of junk.
type Count struct {
	Address string
	Inbox   int
	Spam    int
}

// Unread is everything not yet read for this account, wherever it sits.
func (c Count) Unread() int { return c.Inbox + c.Spam }

// Unread counts what has not been read, per account and in total.
//
// INBOX and the account's spam folder, which is the pair worth watching: mail that a filter got
// wrong is unread mail you have not seen, and a bar that ignores it is a bar that says zero on the
// morning something important was filed as junk.
func Unread(c config.Config, accounts []account.Account) (total int, per []Count) {
	for _, a := range accounts {
		local := a.LocalFolders()
		n := Count{Address: a.Address, Inbox: unreadIn(filepath.Join(a.Maildir(c), "INBOX"))}
		// An account whose server named no spam folder has none to count. Guessing at a name
		// would report zero for a folder that exists under another spelling, which reads the
		// same as a folder that is empty.
		if local.Spam != "" {
			n.Spam = unreadIn(filepath.Join(a.Maildir(c), local.Spam))
		}
		per = append(per, n)
		total += n.Unread()
	}
	return total, per
}

// Split adds up a set of counts into the two figures a status bar shows.
func Split(per []Count) (in, spam int) {
	for _, p := range per {
		in += p.Inbox
		spam += p.Spam
	}
	return in, spam
}

// unreadIn counts the unread messages of one maildir.
//
// Both halves of the maildir, because "unread" lives in two places: everything in new/ is unread by
// definition, and a message moved to cur/ is unread until the S flag is added to its FILENAME.
// Counting only new/ — which is what the old script did — reports zero for every message a client
// has looked at without opening.
func unreadIn(dir string) int {
	n := 0
	for _, e := range readDir(filepath.Join(dir, "new")) {
		if !strings.HasPrefix(e, ".") {
			n++
		}
	}
	for _, e := range readDir(filepath.Join(dir, "cur")) {
		if strings.HasPrefix(e, ".") {
			continue
		}
		// "1786470544.2336075_7.host,U=7:2,S" — the flags follow the last ":2," and S is seen.
		i := strings.LastIndex(e, ":2,")
		if i < 0 || !strings.ContainsRune(e[i+3:], 'S') {
			n++
		}
	}
	return n
}

func readDir(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// Mailbox is one maildir's two numbers, as a sidebar row shows them.
type Mailbox struct {
	// Total is every message in the mailbox, read or not.
	Total int
	// Unread is what has not been read, counted the same way Unread does it.
	Unread int
}

// Measure reads one maildir. NOT named Count, because Count is already the per-account figure in
// this package and two things called the same name in one file is how the wrong one gets called.
//
// A directory that is not there is zero and no error: a folder the server has but mbsync has not
// fetched yet is an empty row, which is what it is.
func Measure(dir string) Mailbox {
	m := Mailbox{}
	for _, e := range readDir(filepath.Join(dir, "new")) {
		if strings.HasPrefix(e, ".") {
			continue
		}
		m.Total++
		m.Unread++ // everything in new/ is unread by definition
	}
	for _, e := range readDir(filepath.Join(dir, "cur")) {
		if strings.HasPrefix(e, ".") {
			continue
		}
		m.Total++
		if i := strings.LastIndex(e, ":2,"); i < 0 || !strings.ContainsRune(e[i+3:], 'S') {
			m.Unread++
		}
	}
	return m
}

/*
Elsewhere is the unread mail of one account that is NOT in its INBOX.

Every maildir under the account except INBOX, DISCOVERED on disk rather than taken from the
registry's list of folders. The registry knows the five this program registers in the sidebar —
Sent, Drafts, Trash, Archive, spam — and a server with a sixth under its own name, Junk being the
common one here, would be silently left out of a number whose whole purpose is "unread you have not
seen yet". A folder nobody registered is exactly where unseen mail hides.

A maildir is recognised the way the format defines one: a directory holding cur or new.
*/
func Elsewhere(c config.Config, a account.Account) int {
	root := a.Maildir(c)
	n := 0
	var walk func(dir string)
	walk = func(dir string) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			sub := filepath.Join(dir, e.Name())
			if e.Name() == "cur" || e.Name() == "new" || e.Name() == "tmp" {
				continue
			}
			if isMaildir(sub) {
				if sub != filepath.Join(root, "INBOX") {
					n += Measure(sub).Unread
				}
			}
			// Deeper regardless: "[Gmail]" is not a maildir itself but holds several.
			walk(sub)
		}
	}
	walk(root)
	return n
}

func isMaildir(dir string) bool {
	for _, half := range []string{"cur", "new"} {
		if fi, err := os.Stat(filepath.Join(dir, half)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}
