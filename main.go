// mailforge sets up NeoMutt and keeps the mail accounts on this machine in agreement.
//
// The problem it exists for: one account's address appears three to eight times across five files
// — the neomutt rc, the mbsync stanza, the maildir path, the password entry — and nothing keeps
// them consistent. Hand-maintained, they drift: a password encrypted to a key that no longer
// exists, a PassCmd commented out years ago, a Sent folder that was renamed on the server. Every
// one of those is invisible until the day it matters.
//
// So the registry is the single source of truth and every file is its output. Nothing is written
// until the account has been proved to work against the real servers.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/inbox"
	"github.com/lvim-tech/mailforge/internal/render"
	"github.com/lvim-tech/mailforge/internal/setup"
	"github.com/lvim-tech/mailforge/internal/sidebar"
	"github.com/lvim-tech/mailforge/internal/sys"
	"github.com/lvim-tech/mailforge/internal/ui"
)

// backupRoot is where a previous hand-made configuration was moved aside, if it was. Only ever
// READ, and only for the colour scheme: carrying somebody's look across is worth doing, and
// carrying their structure across would defeat the point of rebuilding it.
func backupRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "NMUT_BCKP")
}

func main() {
	// Hardening first, before anything can hold a password: it closes the paths by which memory
	// reaches the disk without anyone choosing it, and the guarantee is only worth having if it
	// is in place before the first secret exists.
	sys.Harden()

	c, err := config.Load()
	if err != nil {
		// Reported, not swallowed. A config that cannot be parsed means the program is about to
		// behave as if fields the user set were never set, and finding that out later — from a
		// file written to the wrong place — is much more expensive than reading a line now.
		fmt.Fprintln(os.Stderr, "mailforge:", err)
		fmt.Fprintln(os.Stderr, "  fix or move", config.Path(), "and start again")
		os.Exit(1)
	}

	// `mailforge unread` — a number, and nothing else on stdout.
	//
	// A SUBCOMMAND rather than a second little program, because the answer depends on the
	// registry: which accounts exist, where their maildirs are, what this server calls the spam
	// folder. Anything outside this binary would have to carry a copy of all three and would be
	// wrong the first time one of them moved — which is exactly what happened to the shell
	// script a status bar, a launcher and an editor widget were all sharing.
	if len(os.Args) > 1 && os.Args[1] == "unread" {
		os.Exit(runUnread(c, os.Args[2:]))
	}

	// `mailforge apply` — re-render every account's files from the registry.
	//
	// The same ApplyAccount the wizard's last step runs, with NO SECRET: passwords are not
	// touched, read or asked for. It exists because the generated files can go stale without any
	// account changing — a renamed pass entry, a moved store, an edited template — and the only
	// way to bring them back was to walk each account through a wizard that verifies against the
	// real servers, which is a network round trip and a password prompt to rewrite a text file.
	if len(os.Args) > 1 && os.Args[1] == "apply" {
		os.Exit(runApply(c))
	}

	// `mailforge fetch` — mbsync for every account, then whoever is watching is told.
	//
	// The timer and the key in NeoMutt both call this rather than mbsync directly, so the
	// "and then tell the bar" half cannot end up on only one of the two paths.
	if len(os.Args) > 1 && os.Args[1] == "fetch" {
		os.Exit(setup.Fetch(c))
	}

	// `mailforge sidebar` — the folded sidebar's two halves, both called from inside NeoMutt.
	//
	// A subcommand for the same reason `unread` is one: the answer depends on the registry. Which
	// accounts exist, in what order, and where their maildirs are is what the file it writes is
	// made of, and anything outside this binary would be carrying a second copy of all three.
	if len(os.Args) > 1 && os.Args[1] == "sidebar" {
		os.Exit(runSidebar(c, os.Args[2:]))
	}

	// First run: a machine with no config has no directories, no neomuttrc and nothing for the
	// account wizard to write into. Doing it here rather than behind a screen the user has to
	// find means the first thing they see is a program that already works — and every step is
	// idempotent, so a second run of it is inert rather than destructive.
	if !config.Exists() {
		plan := setup.FirstRun(c, setup.CarrySource(backupRoot()))
		for _, line := range setup.Describe(plan) {
			fmt.Fprintln(os.Stderr, line)
		}
		if plan.Failed() {
			fmt.Fprintln(os.Stderr, "mailforge: setup did not finish; nothing else will be attempted")
			os.Exit(1)
		}
		if c2, err := config.Load(); err == nil {
			c = c2
		}
	}

	p := tea.NewProgram(ui.New(c), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "mailforge:", err)
		os.Exit(1)
	}
}

// runUnread prints the unread count. Plain by default, one line per account with --per-account,
// and a status-bar object with --json.
//
// The two output modes fail DIFFERENTLY, on purpose. Plain prints nothing and returns non-zero: a
// bar reading a bare number substitutes its own text for a command that failed, and a "0" on a
// broken setup is worse than an error — it is the same thing the bar shows when the mail really has
// all been read. A bar reading JSON has no such fallback; an empty line renders no module at all,
// which is that same silent "0" by another route. So --json always prints, and says "!".
// runApply rewrites what every account owns, and reports what changed.
//
// Accounts are applied in registry order and a failure on one does not stop the rest: they own
// separate files, and a machine where four accounts are correct and one is not is strictly better
// than one where the first failure left the other four untouched.
func runApply(c config.Config) int {
	reg, err := account.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mailforge:", err)
		return 1
	}
	if len(reg.Accounts) == 0 {
		fmt.Println("no accounts to apply")
		return 0
	}
	code := 0
	report := func(steps []setup.Step) {
		for _, s := range steps {
			switch {
			case s.Err != nil:
				fmt.Printf("  FAIL  %s: %v\n", s.What, s.Err)
				code = 1
			case s.Note != "":
				fmt.Printf("  ok    %s — %s\n", s.What, s.Note)
			default:
				fmt.Printf("  ok    %s\n", s.What)
			}
		}
	}

	for _, a := range reg.Accounts {
		fmt.Println(a.Address)
		// nil secret: this is a re-render, not a password change.
		report(setup.ApplyAccount(c, &reg, a, nil).Steps)
	}

	// ONCE, after the loop. This half is a function of the config, not of an account, and it
	// used to run inside the loop: the file writes only said "already up to date" five times
	// over, but ApplyTimer restarts the unit, and five restarts in two seconds is past what
	// systemd allows — the last one left the timer FAILED. An apply that exists to schedule
	// mail fetching ended by unscheduling it.
	fmt.Println("configuration")
	var p setup.Plan
	setup.ApplyConfig(c, &p)
	report(p.Steps)

	return code
}

func runUnread(c config.Config, args []string) int {
	asJSON, watch := false, false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "--watch":
			watch = true
		}
	}
	if watch {
		return watchUnread(c, asJSON)
	}

	reg, err := account.Load()
	if err != nil {
		// The bar gets an object saying so; a plain caller gets the line it expects. Exiting
		// quietly here would leave the bar rendering nothing, which reads as "no mail".
		if asJSON {
			return printBarJSON(0, nil, err)
		}
		fmt.Fprintln(os.Stderr, "mailforge:", err)
		return 1
	}
	total, per := inbox.Unread(c, reg.Accounts)

	for _, a := range args {
		switch a {
		case "--per-account":
			for _, p := range per {
				fmt.Printf("%d\t%s\n", p.Unread(), p.Address)
			}
			return 0
		case "--json":
			return printBarJSON(total, per, nil)
		}
	}
	fmt.Println(total)
	return 0
}

// barStatus is what a status bar reads: Waybar's custom-module shape, and anything else that wants
// a machine-readable answer.
//
// It exists so the bar does not need a wrapper script. The registry decides which accounts there
// are, where their maildirs sit and what this server calls the spam folder; a shell script in the
// bar's configuration would have to carry a copy of all three and be wrong the first time one of
// them moved — which is what happened to the script this program's `unread` was written to
// replace. Emitting the bar's own format keeps that knowledge in one place.
type barStatus struct {
	Text    string `json:"text"`
	Tooltip string `json:"tooltip"`
	Class   string `json:"class"`
	// Alt is the state name, and it is how the bar picks an icon.
	//
	// The ICON stays in the bar's configuration, keyed off this, rather than being written into
	// Text here. Which glyph means "mail waiting" depends on the font installed and on the taste
	// of whoever built the bar; what this program knows is whether mail is waiting. Sending the
	// state and letting format-icons choose keeps each decision where it can actually be made.
	Alt string `json:"alt"`
}

// printBarJSON writes one status object, and ALWAYS writes a valid one.
//
// Including on failure. A bar whose command exits non-zero renders nothing at all, so a broken
// registry would look exactly like an empty inbox — the one reading a status bar exists to prevent.
// An error becomes a visible "!" with the reason in the tooltip instead.
func printBarJSON(total int, per []inbox.Count, err error) int {
	var s barStatus
	switch {
	case err != nil:
		s = barStatus{Text: "!", Tooltip: err.Error(), Class: "error", Alt: "error"}
	case len(per) == 0:
		s = barStatus{Text: "No unread", Tooltip: "no accounts", Alt: "read"}
	default:
		in, spam := inbox.Split(per)
		// TWO numbers, because they are two different facts. Mail in INBOX is people waiting;
		// mail in spam is a decision a filter made for you, and it is on the bar so a wrong one
		// is visible. "3" alone cannot tell you which of those you are looking at.
		s.Text = fmt.Sprintf("%d/%d Unread", in, spam)
		s.Alt = "unread"
		s.Class = "unread"
		if total == 0 {
			// Said in words rather than as "0/0". A bar is read at a glance and from the corner
			// of an eye, and "nothing to do" should not need arithmetic.
			s.Text = "No unread"
			s.Alt = "read"
			s.Class = ""
		}
		lines := make([]string, 0, len(per))
		for _, p := range per {
			lines = append(lines, fmt.Sprintf("%d/%d\t%s", p.Inbox, p.Spam, p.Address))
		}
		s.Tooltip = strings.Join(lines, "\n")
	}
	b, merr := json.Marshal(s)
	if merr != nil {
		// Unreachable with these fields, but a bar reading a half-written line is worse than
		// one reading a minimal valid object.
		fmt.Println(`{"text":"!","tooltip":"status could not be encoded","class":"error"}`)
		return 1
	}
	fmt.Println(string(b))
	if err != nil {
		return 1
	}
	return 0
}

// watchUnread prints the count once, and again whenever it changes, until it is killed.
//
// A STREAM instead of a signal, and the reason is a bar that died. Waybar refreshes a module on
// SIGRTMIN+N, but the default action for a real-time signal nothing is listening for is to
// TERMINATE the process — so the same command that updates a correctly configured bar kills one
// whose config was edited, or reloaded, or simply started before the module existed. That is not
// a bug to be careful around; it is a mechanism that fails destructively, and it took the user's
// desktop down every five minutes until the timer was found.
//
// Polling here rather than watching with inotify because the cost was measured, not guessed: one
// pass over every maildir is a quarter of a millisecond, so two seconds between passes is a
// rounding error, and there is no dependency and no watch to lose on a directory that gets
// replaced underneath it.
func watchUnread(c config.Config, asJSON bool) int {
	// A pair that cannot occur, so the first look always counts as a change and prints. Starting
	// at 0/0 would leave a bar blank until the first mail arrived — on a machine with nothing
	// unread, that is forever.
	lastIn, lastSpam := -1, -1
	for {
		reg, err := account.Load()
		if err == nil {
			total, per := inbox.Unread(c, reg.Accounts)
			// BOTH numbers, because either can move on its own: a message read in INBOX and a
			// message caught by a filter in the same interval cancel out in the total, and a
			// bar comparing only that would show the old split until something else changed.
			if in, spam := inbox.Split(per); in != lastIn || spam != lastSpam {
				lastIn, lastSpam = in, spam
				if asJSON {
					printBarJSON(total, per, nil)
				} else {
					fmt.Println(total)
				}
			}
		}
		// Polling, not inotify. What marks a message read is a RENAME inside cur/ — NeoMutt
		// adds the S flag to the filename — so watching would mean a descriptor on every
		// maildir of every account, re-established whenever mbsync creates a folder. Two
		// seconds is under the threshold at which a status bar feels stale, and the work is a
		// directory listing that the page cache has already got.
		time.Sleep(2 * time.Second)
	}
}

/*
runSidebar folds accounts open and shut, and records which one is being read.

	mailforge sidebar current <address>   a folder-hook, on every change of folder
	mailforge sidebar inbox               a second hook, when the folder is an INBOX
	mailforge sidebar counts              the timeout-hook, every few idle seconds
	mailforge sidebar toggle [address]    the Enter macro, on a keypress
	mailforge sidebar render              put the file back the way the state says

SILENT ON STDOUT, all three. `current` is run from a backtick inside muttrc, so anything printed
would be parsed as the value of a NeoMutt variable; `toggle` is run from <shell-escape>, which puts
what it prints on the screen the user is reading mail on.

`current` swallows its errors as well, and that is the one place here worth arguing about. Its
failure mode is that Enter does not know which account to fold — annoying, and fixable by opening a
folder again. Its alternative is a line of diagnostics across the pager every time the folder
changes, from a program the user did not run. The doctor is where a broken state directory gets
reported, not here.
*/
func runSidebar(c config.Config, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr,
			"usage: mailforge sidebar current <address> | inbox | counts | toggle [address] | render")
		return 2
	}

	switch args[0] {
	case "current":
		if len(args) < 2 {
			return 2
		}
		_ = sidebar.SetCurrent(args[1])
		return 0

	case "inbox":
		_ = sidebar.SetCurrentIsInbox()
		return 0

	case "counts":
		// The timeout-hook's half: recount the mail and rewrite the labels, nothing else. No
		// unmailboxes, no push, no state written — see render.CountsRC for why re-registering
		// the list here would fight the user for the sidebar's scroll position.
		//
		// Silent and zero even when it fails: this runs every few seconds from inside a client
		// somebody is reading mail in, and a count that could not be taken is a stale number,
		// not an incident. The doctor is where a missing file gets reported.
		reg, err := account.Load()
		if err != nil {
			return 0
		}
		st := sidebar.Load()
		body := render.CountsRC(c, reg.Accounts, st.OpenSet(), setup.SidebarCounts(c, reg.Accounts))
		p := render.CountsPath(c)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return 0
		}
		_ = config.WriteAtomic(p, []byte(body), 0o600)
		return 0

	case "toggle":
		st := sidebar.Load()
		addr := st.Current
		named := len(args) > 1
		if named {
			addr = args[1]
		}
		if addr == "" {
			fmt.Fprintln(os.Stderr, "mailforge:", sidebar.ErrNoAccount)
			return 1
		}
		// Only an account's own row folds. The key opened the highlighted row before getting
		// here, so a plain folder under an unfolded account would otherwise fold the account
		// shut and hide the folder that was just opened — Enter would refuse to let you in.
		//
		// An address given by hand folds regardless: that is somebody typing the command, not
		// the key, and they have said which account they mean.
		if named || st.CurrentIsInbox {
			st.Toggle(addr)
		}
		if err := writeSidebar(c, &st); err != nil {
			fmt.Fprintln(os.Stderr, "mailforge:", err)
			return 1
		}
		return 0

	case "render":
		st := sidebar.Load()
		if err := writeSidebar(c, &st); err != nil {
			fmt.Fprintln(os.Stderr, "mailforge:", err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(os.Stderr, "mailforge: no such sidebar command:", args[0])
	return 2
}

// writeSidebar renders the file and saves the state that produced it, in that order.
//
// The file first, because it is the half NeoMutt reads: a state file saying an account is folded
// open while the sidebar shows it shut is a disagreement the next keypress corrects, and the
// reverse — a sidebar that cannot be folded back because the state was never written — is not.
func writeSidebar(c config.Config, st *sidebar.State) error {
	reg, err := account.Load()
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(reg.Accounts))
	for _, a := range reg.Accounts {
		known[a.Address] = true
	}
	st.Prune(known)

	p := render.SidebarPath(c)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	body := render.SidebarRC(c, reg.Accounts, st.OpenSet(), st.Current, setup.SidebarCounts(c, reg.Accounts))
	if err := config.WriteAtomic(p, []byte(body), 0o600); err != nil {
		return err
	}
	return st.Save()
}
