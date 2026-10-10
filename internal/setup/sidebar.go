package setup

import (
	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/inbox"
	"github.com/lvim-tech/mailforge/internal/render"
)

/*
SidebarCounts reads the mail and hands the renderer its numbers.

It sits HERE rather than in render because render does no I/O — that is what makes every generated
file golden-testable — and rather than in inbox because inbox answers a different question for a
status bar and knows nothing about sidebar rows. This is the one place where the two meet, and it is
three lines of arithmetic over functions that both already had.

The keys are mailbox paths, spelled the same way render spells them. Not filepath.Join: it would
clean the path, and a key that differs from the one looked up by a single character is a count that
silently reads as zero.
*/
func SidebarCounts(c config.Config, accounts []account.Account) map[string]render.SidebarCount {
	out := make(map[string]render.SidebarCount, len(accounts)*6)
	for _, a := range accounts {
		dir := a.Maildir(c)

		ib := inbox.Measure(dir + "/INBOX")
		out[dir+"/INBOX"] = render.SidebarCount{
			Total:  ib.Total,
			Unread: ib.Unread,
			// The number NeoMutt cannot produce, which is the reason any of this exists.
			Elsewhere: inbox.Elsewhere(c, a),
		}

		for _, f := range render.FolderNames(a) {
			m := inbox.Measure(dir + "/" + f)
			out[dir+"/"+f] = render.SidebarCount{Total: m.Total, Unread: m.Unread}
		}
	}
	return out
}
