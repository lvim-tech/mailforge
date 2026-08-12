package setup

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/edit"
	"github.com/lvim-tech/mailforge/internal/render"
)

// FirstRun prepares a machine that has no mail configuration at all.
//
// What it creates it creates ONCE and then never touches again: neomuttrc and the three user files
// beneath it are seeds, not templates. The only part of that tree mailforge revisits is the single
// marker block inside neomuttrc, so the user can rewrite everything else without ever having to
// wonder whether their work will survive an apply.
//
// The seeds are deliberately small. A settings file full of somebody else's opinions is a file
// nobody reads and everybody keeps; a short one is a file people actually edit.
func FirstRun(c config.Config, carryColorsFrom string) Plan {
	var p Plan
	run := func(what string, fn func() (string, error)) {
		s := Step{What: what}
		note, err := fn()
		s.Done, s.Note, s.Err = err == nil, note, err
		p.Steps = append(p.Steps, s)
	}

	for _, d := range []string{
		c.MaildirRoot,
		filepath.Join(c.NeomuttDir, "mailforge", "accounts"),
		cacheDir(),
	} {
		dir := d
		run("create "+dir, func() (string, error) {
			if _, err := os.Stat(dir); err == nil {
				return "already there", nil
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", err
			}
			return "created", nil
		})
	}

	for _, s := range Seeds(c) {
		name, body := s.Name, s.Body
		if name == "colors.rc" {
			body = carryColors(LiveTheme(c.NeomuttDir), carryColorsFrom)
		}
		path := filepath.Join(c.NeomuttDir, name)
		run("seed "+path, func() (string, error) {
			if _, err := os.Stat(path); err == nil {
				// Never overwritten. These belong to the user from the moment they exist,
				// and a "setup" that silently replaces somebody's colour scheme is a setup
				// nobody runs twice.
				return "already there, left alone", nil
			}
			return "written", config.WriteAtomic(path, []byte(body), 0o600)
		})
	}

	rc := filepath.Join(c.NeomuttDir, "neomuttrc")
	run("write "+rc, func() (string, error) {
		if _, err := os.Stat(rc); err != nil {
			// The seed carries the marker block already. Writing the file and then editing it
			// would have the engine treat a file one line old as foreign and take a backup of
			// it — a copy of something this program wrote a millisecond earlier.
			if err := config.WriteAtomic(rc, []byte(seedNeomuttrc(c)), 0o600); err != nil {
				return "", err
			}
			return "created", nil
		}
		// An existing neomuttrc gains the include without losing anything else.
		res, err := edit.Upsert(rc, "", render.NeomuttInclude(c), false)
		if err != nil {
			return "", err
		}
		return res.Note, nil
	})

	run("write "+config.Path(), func() (string, error) {
		if config.Exists() {
			return "already there", nil
		}
		return "written", config.Save(c)
	})

	return p
}

// Seed is a file FirstRun lays down for the user and then never touches again.
type Seed struct {
	Name string
	Body string
	Why  string // what breaks while it is absent, for the doctor to say
}

// Seeds lists them, and is shared with the audit rather than kept private to FirstRun.
//
// FirstRun is gated on the config file not existing, so it happens exactly once per machine. A
// seed added to the program afterwards can therefore never arrive on a machine that is already set
// up — and nothing would say so, because the file it is missing is one nobody thinks to look for.
// That is not hypothetical: mailcap was added after this machine's first run, settings.rc had been
// pointing mailcap_path at it ever since, and the doctor was green throughout.
func Seeds(c config.Config) []Seed {
	const sourced = "neomuttrc sources it, so NeoMutt reports the missing file at every start " +
		"and whatever it was meant to configure is simply not configured"
	return []Seed{
		{"settings.rc", seedSettings(c), sourced},
		{"colors.rc", carryColors(LiveTheme(c.NeomuttDir), ""), sourced},
		{"bindings.rc", seedBindings(), sourced},
		{"mailcap", seedMailcap(), "settings.rc points mailcap_path at it, and without it an " +
			"attachment cannot be opened"},
	}
}

func cacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); d != "" {
		return filepath.Join(d, "neomutt")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "neomutt")
}

func seedNeomuttrc(c config.Config) string {
	// Two managed blocks, and their positions are the design rather than a layout choice.
	//
	// KEYS FIRST: NeoMutt keeps the last binding it reads, so bindings.rc below overrides
	// anything here — which is what makes the managed set a starting point rather than a cage.
	// ACCOUNTS LAST: accounts.rc ends by sourcing the default account's identity, and nothing
	// afterwards may quietly reset folder or from.
	return `# Your NeoMutt configuration. mailforge wrote this file once and will not write it
# again — everything here is yours to change.
#
# The only exceptions are the marked blocks, which point at the files mailforge
# generates. Leave the markers where they are and it will keep itself correct.

` + edit.Open("options") + "\n" +
		"# Managed by mailforge. Sourced first on purpose: your settings.rc below overrides it.\n" +
		render.NeomuttOptionsInclude(c) +
		edit.Close("options") + `

` + edit.Open("keys") + "\n" +
		"# Managed by mailforge. Sourced first on purpose: your bindings.rc below overrides it.\n" +
		render.NeomuttKeysInclude(c) +
		edit.Close("keys") + `

source ~/.config/neomutt/settings.rc
source ~/.config/neomutt/colors.rc
source ~/.config/neomutt/bindings.rc

` + edit.Open("") + "\n" +
		"# Managed by mailforge. Everything between these markers is rewritten on apply; edits here are lost.\n" +
		render.NeomuttInclude(c) +
		edit.Close("") + "\n"
}

func seedSettings(c config.Config) string {
	var b strings.Builder
	b.WriteString("# Settings that are yours. mailforge seeded this once and never touches it again.\n\n")
	// The backend has to be named BEFORE the cache path: NeoMutt refuses the second option
	// otherwise, with "Set option header_cache_backend before setting header_cache" — and the
	// cache then quietly does not exist, which shows up as a client that is slow forever.
	b.WriteString("set header_cache_backend = \"gdbm\"\n")
	b.WriteString("set header_cache  = \"" + filepath.Join(cacheDir(), "headers") + "\"\n")
	b.WriteString("set message_cachedir = \"" + filepath.Join(cacheDir(), "bodies") + "\"\n\n")
	b.WriteString(`set sort           = threads
set sort_aux       = reverse-last-date-received
set mailcap_path   = ~/.config/neomutt/mailcap

# Mail is fetched by mbsync into the maildirs, so NeoMutt never speaks IMAP itself.
unset imap_passive
set sidebar_visible
set sidebar_width  = 32
set sidebar_short_path
set sidebar_format = "%D%* %?N?%N/?%S"
`)
	return b.String()
}

// seedMailcap is the minimum that stops NeoMutt complaining and makes HTML mail readable.
//
// `copiousoutput` is what lets the text version appear inline rather than launching something;
// without it an HTML message opens a browser, which on a mail client used over ssh is a surprise.
func seedMailcap() string {
	return `# How to view attachments. mailforge seeded this once and never touches it again.

text/html; lynx -dump -width=100 %s; nametemplate=%s.html; copiousoutput
text/plain; cat %s; copiousoutput
image/*; xdg-open %s
application/pdf; xdg-open %s
`
}

// seedBindings deliberately seeds NO bindings.
//
// It used to seed four sidebar keys and four movement keys, and that was a mistake worth naming:
// those same actions are now managed in keys.rc, this file is sourced after it, and NeoMutt keeps
// the last binding it reads. A seed here would therefore have silently overridden the managed set
// with different keys — the user would change a binding in the interface, watch the file change,
// and watch nothing happen in NeoMutt.
//
// So the file exists, is explained, and is empty. It is the place for bindings mailforge does not
// manage, and for overriding the ones it does.
func seedBindings() string {
	return `# Key bindings that are yours. mailforge seeded this once and never touches it again.
#
# The bindings mailforge manages live in mailforge/keys.rc, which is sourced BEFORE this
# file — so anything you put here wins. Change them in mailforge (the Keys tab), or
# override one here:
#
#   bind index,pager \Cn sidebar-next
`
}

// carryColors decides what colors.rc says on the day it is created.
//
// Two different things can be found, and they want opposite treatment:
//
//   - A LIVE theme file that something else keeps rewriting — themer fetches the current theme
//     into ~/.config/neomutt/theme.conf every time the desktop's look changes. That one is
//     SOURCED. Copying it was the bug: the copy froze the colours as they stood on the day of
//     setup, so every later theme switch reached the whole desktop and stopped at the mail
//     client, with nothing on screen to say why. themer's own target file says as much —
//     "neomutt sources plain config files, so the theme is one `source` line away".
//
//   - A DEAD backup snapshot under ~/NMUT_BCKP, which nothing will ever rewrite. That one is
//     copied, because there is no live file to follow and the bytes are all that is left.
//
// Either way the file belongs to the user from this moment: mailforge writes it once and never
// again, and NeoMutt keeps the LAST setting it reads, so anything added below the source line
// overrides the theme without touching a generated file.
func carryColors(live, from string) string {
	header := "# Colours. Seeded once from the previous configuration; yours from now on.\n\n"
	const wins = "\n# Anything below wins: NeoMutt keeps the LAST setting it reads, so a line here\n" +
		"# overrides the theme without editing a generated file.\n"

	if live != "" {
		return header +
			"# The live theme, rewritten by whatever manages your desktop's look. Sourced rather\n" +
			"# than copied, so a theme switch reaches NeoMutt instead of stopping at it.\n" +
			"source " + live + "\n" + wins
	}
	if from == "" {
		return header + "# nothing was carried over — this is where a colour scheme goes\n"
	}
	b, err := os.ReadFile(from)
	if err != nil {
		return header + "# " + from + " could not be read: " + err.Error() + "\n"
	}
	return header + string(b)
}

// LiveTheme is the theme file a theme manager keeps current, if there is one.
//
// Existence is the whole test, and it is checked at seed time rather than the line being written
// unconditionally: NeoMutt reports a missing `source` at every start, and an error banner about a
// file the user never asked for is worse than a colours file with nothing in it.
func LiveTheme(neomuttDir string) string {
	p := filepath.Join(neomuttDir, "theme.conf")
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

// CarrySource looks for a colour file worth carrying over, newest convention first.
func CarrySource(backupRoot string) string {
	for _, rel := range []string{
		".config/neomutt/theme.conf",
		"mails/mutt/colors",
	} {
		p := filepath.Join(backupRoot, rel)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}
