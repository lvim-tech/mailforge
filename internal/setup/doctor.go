// Package setup owns the two things that happen outside the account lifecycle: preparing a
// machine that has none, and telling the truth about one that does.
//
// The doctor is not a diagnostic afterthought here — agreement between files IS the product, so
// the check that proves it is a first-class screen. Every finding names what it looked at, so a
// green line is evidence rather than reassurance.
package setup

import (
	"fmt"
	"os"
	"strings"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/store"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// Level is how much a finding matters.
type Level int

const (
	// Fail means something the program needs is absent or wrong; nothing will work until it is
	// fixed, and the finding says how.
	Fail Level = iota
	// Warn means it works but will bite: a version too old for an option that gets rendered, a
	// path that exists but is not what the config says.
	Warn
	// OK is a check that passed, kept visible on purpose. A doctor that only shows problems
	// cannot be used to answer "is this machine ready", only "is it broken".
	OK
)

// Finding is one check, with what was examined and what to do about it.
type Finding struct {
	Level   Level
	Subject string
	Message string
	Action  string // what to do; empty when there is nothing to do, and then nothing is shown
}

// Report is a run of the doctor.
type Report struct {
	Findings []Finding
}

// Counts summarises, for the header line.
func (r Report) Counts() (fail, warn, ok int) {
	for _, f := range r.Findings {
		switch f.Level {
		case Fail:
			fail++
		case Warn:
			warn++
		default:
			ok++
		}
	}
	return
}

// tool is an external program mailforge depends on, and what it is depended on FOR — the second
// half is what makes an absence actionable instead of a shopping list.
type tool struct {
	name    string
	forWhat string
	// versionArgs asks the tool its version. Empty means the tool has no useful answer and
	// presence is all that is checked.
	versionArgs []string
}

var tools = []tool{
	{"pass", "where account passwords live", []string{"version"}},
	{"gpg", "what pass encrypts with", []string{"--version"}},
	{"mbsync", "fetches the mail into the maildirs", []string{"--version"}},
	{"neomutt", "reads it", []string{"-v"}},
}

// Environment checks whether this machine can host the setup at all.
//
// Deliberately separate from the per-account audit: this half answers "can anything work here",
// and it is the half that must run before a single account exists. A missing `pass` is not an
// account problem and reporting it as one would send the reader to the wrong screen.
func Environment(c config.Config) Report {
	var r Report

	for _, t := range tools {
		path := sys.Which(t.name)
		if path == "" {
			r.Findings = append(r.Findings, Finding{
				Fail, t.name,
				"not on PATH — " + t.forWhat,
				"install it, or put it on PATH if it is installed elsewhere",
			})
			continue
		}
		ver := ""
		if len(t.versionArgs) > 0 {
			if res, err := sys.Run(t.name, t.versionArgs...); err == nil {
				ver = versionLine(res.Stdout)
				if ver == "" {
					ver = versionLine(res.Stderr)
				}
			}
		}
		msg := path
		if ver != "" {
			msg = ver + "  ·  " + path
		}
		r.Findings = append(r.Findings, Finding{OK, t.name, msg, ""})
	}

	// The store. Present and initialised are different facts: an uninitialised store is a
	// directory with no .gpg-id in it, and `pass insert` into one fails with a message about
	// the recipient that reads like a gpg problem.
	switch {
	case !store.Available():
		r.Findings = append(r.Findings, Finding{
			Fail, "pass store",
			"not initialised — " + store.Dir() + " has no .gpg-id",
			"run: pass init <your-gpg-key-id>",
		})
	default:
		rec := store.Recipient()
		extra := ""
		if others := store.Recipients(); len(others) > 1 {
			extra = fmt.Sprintf("  (+%d more recipients)", len(others)-1)
		}
		r.Findings = append(r.Findings, Finding{
			OK, "pass store", "encrypts to " + rec + extra + "  ·  " + store.Dir(), "",
		})
	}

	// The CA bundle. mbsync is told to trust this file by name, and a wrong path fails at
	// connection time with a message about certificates that reads like the server's fault.
	if _, err := os.Stat(c.CertFile); err != nil {
		r.Findings = append(r.Findings, Finding{
			Fail, "CA bundle",
			c.CertFile + " does not exist — every rendered mbsync block points at it",
			"set the right path in Settings; on this distribution it is usually /etc/ssl/ca-bundle.pem",
		})
	} else {
		r.Findings = append(r.Findings, Finding{OK, "CA bundle", c.CertFile, ""})
	}

	// The directories mailforge writes into. Absent is not a fault before first run — it is
	// the ordinary state of a machine that has not been set up — so it is reported as the
	// thing setup will do, not as damage.
	for _, d := range []struct{ label, path string }{
		{"maildir root", c.MaildirRoot},
		{"neomutt dir", c.NeomuttDir},
	} {
		st, err := os.Stat(d.path)
		switch {
		case err != nil:
			r.Findings = append(r.Findings, Finding{
				Warn, d.label, d.path + " does not exist yet", "first-run setup creates it",
			})
		case !st.IsDir():
			r.Findings = append(r.Findings, Finding{
				Fail, d.label, d.path + " exists and is not a directory", "move it aside",
			})
		default:
			r.Findings = append(r.Findings, Finding{OK, d.label, d.path, ""})
		}
	}

	// mbsyncrc is foreign: mailforge edits it between markers and owns nothing else in it.
	// Absent is fine — the first account creates it — but a directory in its place is not.
	if st, err := os.Stat(c.MbsyncRC); err == nil && st.IsDir() {
		r.Findings = append(r.Findings, Finding{
			Fail, "mbsyncrc", c.MbsyncRC + " is a directory", "move it aside",
		})
	} else if err != nil {
		r.Findings = append(r.Findings, Finding{
			Warn, "mbsyncrc", c.MbsyncRC + " does not exist yet", "the first account creates it",
		})
	} else {
		r.Findings = append(r.Findings, Finding{OK, "mbsyncrc", c.MbsyncRC, ""})
	}

	// Memory hardening, REPORTED rather than assumed. Harden runs before anything can hold a
	// password and returns what it managed; leaving that report unread is the silent
	// non-protection it exists to prevent, while the password field goes on saying "locked
	// memory" beside every character typed.
	if done := sys.Hardening(); len(done) < sys.HardenSteps {
		got := "nothing"
		if len(done) > 0 {
			got = strings.Join(done, ", ")
		}
		r.Findings = append(r.Findings, Finding{
			Warn, "memory hardening",
			"the kernel accepted " + got + " — a crash or a debugger could still read a password out of this process",
			"check the limits this user runs under (ulimit -c, /proc/sys/kernel/yama)",
		})
	} else {
		r.Findings = append(r.Findings, Finding{OK, "memory hardening", strings.Join(done, ", "), ""})
	}

	// Swap is the other path from memory to disk, and nothing in this program can close it. Say
	// so rather than imply a guarantee that stops at the swap device.
	if w := sys.SwapWarning(); w != "" {
		r.Findings = append(r.Findings, Finding{
			Warn, "swap", w,
			"encrypt the swap device, or accept that a password may reach it",
		})
	}

	// Config file: its absence is what triggers first-run, so it is worth stating plainly
	// rather than leaving the reader to infer it from a screen they have not seen yet.
	if !config.Exists() {
		r.Findings = append(r.Findings, Finding{
			Warn, "mailforge config",
			"not written yet — running on defaults",
			"Settings, then [w]",
		})
	} else {
		r.Findings = append(r.Findings, Finding{OK, "mailforge config", config.Path(), ""})
	}

	return r
}

// versionLine picks the line that actually carries the version out of what a tool prints.
//
// The first line is not it often enough to matter: `pass version` draws an ASCII box, and its
// first line is a row of equals signs. So the rule is the first line containing a digit, with the
// box drawing stripped off it — which lands on "v1.7.4" for pass and leaves "gpg (GnuPG) 2.5.21",
// "isync 1.5.1" and "NeoMutt 20251211-1035-2ce686" as they are.
func versionLine(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "="))
		if l == "" || !strings.ContainsAny(l, "0123456789") {
			continue
		}
		return strings.TrimSpace(l)
	}
	return ""
}
