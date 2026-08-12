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

// Count is one account's unread total.
type Count struct {
	Address string
	Unread  int
}

// Unread counts what has not been read, per account and in total.
//
// INBOX and the account's spam folder, which is the pair worth watching: mail that a filter got
// wrong is unread mail you have not seen, and a bar that ignores it is a bar that says zero on the
// morning something important was filed as junk.
func Unread(c config.Config, accounts []account.Account) (total int, per []Count) {
	for _, a := range accounts {
		n := 0
		local := a.LocalFolders()
		for _, box := range []string{"INBOX", local.Spam} {
			if box == "" {
				continue
			}
			n += unreadIn(filepath.Join(a.Maildir(c), box))
		}
		per = append(per, Count{Address: a.Address, Unread: n})
		total += n
	}
	return total, per
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
