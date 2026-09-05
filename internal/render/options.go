package render

import (
	"fmt"
	"strings"

	"github.com/lvim-tech/mailforge/internal/config"
)

// OptionsRC renders the NeoMutt settings that come from mailforge's own config rather than from an
// account.
//
// Its own file, and sourced BEFORE the user's settings.rc, for the same reason keys.rc is: what
// this program manages has to be rewritable on every apply, and what the user writes by hand has to
// win. A setting here is a starting point, not a cage — one line in their file overrides it.
//
// That ordering is also why the seed no longer carries `set editor`. The seeded line was mailforge's
// own, it expanded $EDITOR once at parse time, and being in the user's file it beat anything this
// program could later put right.
func OptionsRC(c config.Config) string {
	var b strings.Builder
	b.WriteString(HeaderFromConfig)
	b.WriteString("#\n")
	b.WriteString("# Change these in mailforge, in the Settings tab. Anything you set in your own\n")
	b.WriteString("# settings.rc is read after this file and therefore wins.\n\n")

	if e := strings.TrimSpace(c.Editor); e != "" {
		fmt.Fprintf(&b, "set editor = %s\n", quote(e))
	} else {
		// Said out loud rather than left as an empty file, because "nothing here" and "this
		// program has no opinion here" look identical on disk and mean different things.
		b.WriteString("# no editor set in mailforge — NeoMutt falls back to $VISUAL, $EDITOR, then vi\n")
	}

	// What the seeded sidebar_format needs to have anything to show. Written as a pair, because
	// the interval alone is meaningless and the flag alone leaves NeoMutt's own minute in place
	// — and a reader of this file should not have to know which of the two was left implicit.
	b.WriteString("\n")
	if c.SidebarCounts {
		b.WriteString("set mail_check_stats\n")
		fmt.Fprintf(&b, "set mail_check_stats_interval = %d\n", c.SidebarCountsEvery)
	} else {
		b.WriteString("unset mail_check_stats   # the sidebar's %N and %S have nothing to count\n")
	}
	// The three nested timers, written together because they are read together: NeoMutt waits
	// `timeout` for a key, then looks for mail if `mail_check` has elapsed, and only inside that
	// look are the counts recomputed if `mail_check_stats_interval` has. Each is a ceiling over
	// the next. They stay three settings — a recount is real work on a large maildir and a
	// new-mail check is not — and the interface shows what each one EFFECTIVELY comes to.
	fmt.Fprintf(&b, "set timeout = %d\n", c.WakeEvery)
	fmt.Fprintf(&b, "set mail_check = %d\n", c.CheckEvery)

	// Do not stop for a key press after a shell command that WORKED.
	//
	// From NeoMutt's own source: it waits when `rc != 0 || wait_key`, so unsetting this removes
	// the pause on success and keeps it on failure — which is the only time the output is worth
	// reading. The fetch macro is the reason: pressing a key to fetch mail and then pressing
	// another to get back to the mailbox is one key too many for something done all day.
	b.WriteString("unset wait_key   # only pause after a command that failed\n")

	// Which headers a message actually shows.
	//
	// NeoMutt weeds by default, but its ignore list STARTS EMPTY — so the weeding runs and
	// removes nothing, and a message opens on Received:, ARC-Seal:, DKIM-Signature: and a
	// screenful of X-Gm-* before a word of what somebody wrote.
	//
	// Written as "hide everything, then name what is worth reading" rather than as a list of
	// what to hide: the headers worth reading are a short, stable set, and the ones worth
	// hiding are unbounded — every provider invents more of them, and a blocklist is out of
	// date the first time one does.
	//
	// Still only a DEFAULT, like everything else in this file: it is sourced before the user's
	// settings.rc, and ignore/unignore accumulate, so `unignore List-Id:` there brings one back.
	b.WriteString("\n")
	if keep := headerList(c.KeepHeaders); keep != "" {
		b.WriteString("ignore *\n")
		fmt.Fprintf(&b, "unignore %s\n", keep)
		// And in a fixed order, because without this they appear in whatever order the sending
		// server happened to write them, which differs per message and makes the top of a mail
		// something to read rather than something to glance at.
		b.WriteString("unhdr_order *\n")
		fmt.Fprintf(&b, "hdr_order %s\n", keep)
	} else {
		// Said out loud, for the same reason the missing editor is: an empty list and "this
		// program has no opinion" look identical on disk and mean different things.
		b.WriteString("# no headers kept in mailforge — every header is shown, as NeoMutt does by default\n")
	}

	// Showing an HTML message in the pager.
	//
	// Two halves, and mailforge used to seed one of them. The mailcap rule says HOW to turn the
	// part into text; `auto_view` says to DO it for the body rather than leave the part to be
	// opened by hand with `v`. Without the second the first is never reached, so a mailbox full
	// of HTML reads as a mailbox full of empty messages — which is what happened here, for
	// months, with a mailcap rule sitting in place the whole time.
	//
	// alternative_order is written out although it matches NeoMutt's own default: it is the half
	// of this that is easy to lose, and a message carrying both parts should be read as the text
	// one.
	b.WriteString("\n")
	if program, _ := c.HTMLViewerCommand(); program != "" {
		fmt.Fprintf(&b, "# HTML mail is turned into text by %s — see mailforge/mailcap\n", program)
		// Both files, the user's first, because mailcap takes the FIRST match.
		b.WriteString(NeomuttMailcapPath(c))
		b.WriteString("auto_view text/html\n")
		b.WriteString("alternative_order text/plain text/enriched text/html\n")
	} else {
		// Said out loud rather than left silent, for the same reason as the missing editor:
		// nothing here and "this program has no opinion here" look identical on disk.
		b.WriteString("# no HTML viewer available — HTML mail stays an attachment, opened with `v`\n")
		b.WriteString("# install w3m (or lynx), then apply again\n")
	}

	return b.String()
}

/*
MailcapRC is mailforge's own mailcap, and it is a SECOND file rather than a line in the user's.

mailcap_path takes a list and the FIRST match wins, which is the opposite of how NeoMutt reads its
source lines — so this file goes AFTER the user's, and anything they write there still beats it.
The effect is the one options.rc has for settings: what mailforge manages is rewritten on every
apply, and what the user writes by hand wins.

Empty when no viewer resolves. A mailcap rule naming a program that is not installed is worse than
no rule at all: NeoMutt finds a handler, runs it, gets nothing back, and shows an empty message
with no indication that anything went wrong.
*/
func MailcapRC(c config.Config) string {
	program, command := c.HTMLViewerCommand()
	if program == "" {
		return HeaderFromConfig +
			"#\n# No HTML viewer is available or configured, so there is no rule here.\n" +
			"# Install w3m (or lynx) and apply again, or set one in the Settings tab.\n"
	}
	return HeaderFromConfig +
		"#\n# Read AFTER your own mailcap: the first matching line wins, so anything you\n" +
		"# write there overrides this.\n\n" +
		"text/html; " + command + "; nametemplate=%s.html; copiousoutput\n"
}

// NeomuttMailcapPath points NeoMutt at both files, the user's first.
func NeomuttMailcapPath(c config.Config) string {
	return fmt.Sprintf("set mailcap_path = %s/mailcap:%s/mailforge/mailcap\n", c.NeomuttDir, c.NeomuttDir)
}

// headerList renders the keep list as one NeoMutt argument line.
//
// Entries are trimmed and the blank ones dropped, because this list is edited as a line of text in
// the settings screen and a stray double space there would otherwise become an empty pattern —
// which NeoMutt reads as a pattern matching everything, quietly undoing the `ignore *` above it.
func headerList(keep []string) string {
	out := make([]string, 0, len(keep))
	for _, h := range keep {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return strings.Join(out, " ")
}

// NeomuttOptionsInclude is the line that sources it, and its block belongs among the first.
func NeomuttOptionsInclude(c config.Config) string {
	return fmt.Sprintf("source %s/mailforge/options.rc\n", c.NeomuttDir)
}
