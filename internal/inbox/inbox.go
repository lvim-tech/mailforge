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
