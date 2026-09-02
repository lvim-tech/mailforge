package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/edit"
	"github.com/lvim-tech/mailforge/internal/render"
	"github.com/lvim-tech/mailforge/internal/store"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// Step is one line of the write plan: what will happen, then what did.
//
// The plan is shown before it runs and ticked as it goes, because the alternative is a program
// that says "done" after touching five files in somebody's home directory and leaves them to find
// out which. A step that did nothing says so — "already up to date" is information.
type Step struct {
	What string // shown before it runs
	Done bool
	Note string // what actually happened
	Err  error
}

// Plan is the ordered list of writes for one account.
type Plan struct {
	Steps []Step
}

// Failed reports whether anything went wrong.
func (p Plan) Failed() bool {
	for _, s := range p.Steps {
		if s.Err != nil {
			return true
		}
	}
	return false
}

// ApplyAccount writes everything one account needs, in the order that survives interruption.
//
// THE PASSWORD GOES FIRST. Every other artefact refers to the pass entry by name, so a crash
// midway must leave files pointing at an entry that exists rather than one that does not: the
// first is a working account with a missing rc, the second is an rc that asks for a password
// nobody can produce and fails at the moment mail is wanted.
//
// The registry is written LAST, for the mirror-image reason: it is the source of truth, and a
// registry that lists an account whose files were never written is a lie that the doctor then has
// to unpick. Files first, record after — the state in between is "extra files nothing refers to",
// which is inert and visible.
func ApplyAccount(c config.Config, r *account.Registry, a account.Account, secret *sys.Secret) Plan {
	var p Plan

	// Where the password actually lives, settled ONCE, here, before anything is rendered from it.
	//
	// This is the only place that asks the store a question about names, and it does so because
	// it is also the only place that can record the answer: `a` is rendered into the mbsync block
	// and the account rc below, and written to the registry at the end, so a decision made now
	// reaches all three. Deciding inside the renderer instead would re-ask on every draw and make
	// the generated files depend on the machine they were generated on.
	//
	// A store holding the password under BOTH layouts is left alone deliberately. There is no
	// honest way to pick — the two files are equally real and only their owner knows which is
	// current — so the configured layout stays in force, the audit reports the pair, and the
	// account view asks. Guessing here is exactly what wrote a duplicate into this user's store.
	if a.PassOverride == "" {
		cands := a.PassCandidates(c)
		if store.Duplicated(cands) == nil {
			if found := store.Resolve(cands); found != a.PassEntryIn(c, c.PassLayout) {
				a.PassOverride = found
			}
		}
	}

	entry := a.PassEntry(c)
	run := func(what string, fn func() (string, error)) bool {
		s := Step{What: what}
		note, err := fn()
		s.Done, s.Note, s.Err = err == nil, note, err
		p.Steps = append(p.Steps, s)
		return err == nil
	}

	// 1. The password. Only when one was supplied — editing an account leaves its password
	// alone unless the user typed a new one, and re-writing an unchanged secret would be a
	// gpg round trip for nothing.
	if secret != nil && !secret.Empty() {
		ok := run("store the password as "+entry, func() (string, error) {
			if store.Exists(entry) {
				// ReplacePassword, not Replace: the entry may be older than this account and
				// carry a login, a URL or a note somebody put there. Only line one is ours.
				return "replaced", store.ReplacePassword(entry, secret)
			}
			return "inserted", store.InsertSecret(entry, secret, nil)
		})
		if !ok {
			return p
		}
	}

	// 2. The maildir, AND an empty INBOX inside it.
	//
	// mbsync creates the mailboxes on the first sync, but NeoMutt is openable before that — and
	// a spoolfile of +INBOX pointing at nothing greets the user with "Unable to open mailbox",
	// which reads as a broken setup rather than as an account with no mail fetched yet. An empty
	// maildir is the truthful version of the same state, and mbsync is content to sync into one
	// that already exists.
	//
	// The discovered folders get the same treatment, so the sidebar is navigable immediately
	// instead of listing mailboxes that error when opened.
	if !run("create "+a.Maildir(c), func() (string, error) {
		// LOCAL names. Creating them under the server's spelling is what made mbsync refuse
		// them: a local mailbox whose name contains the server's hierarchy delimiter cannot be
		// mapped back unambiguously, so it reports the directory rather than using it.
		local := a.LocalFolders()
		boxes := []string{"INBOX"}
		for _, f := range []string{local.Sent, local.Drafts, local.Trash,
			local.Archive, local.Spam} {
			if f != "" {
				boxes = append(boxes, f)
			}
		}
		made := 0
		for _, box := range boxes {
			for _, sub := range []string{"cur", "new", "tmp"} {
				if err := os.MkdirAll(filepath.Join(a.Maildir(c), box, sub), 0o700); err != nil {
					return "", err
				}
			}
			made++
		}
		return fmt.Sprintf("%d mailboxes ready", made), nil
	}) {
		return p
	}

	// 3. The per-account rc, owned whole.
	if !run("write "+a.RCFile(c), func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(a.RCFile(c)), 0o700); err != nil {
			return "", err
		}
		body := render.AccountRC(c, a)
		if same, err := unchanged(a.RCFile(c), body); err == nil && same {
			return "already up to date", nil
		}
		return "written", config.WriteAtomic(a.RCFile(c), []byte(body), 0o600)
	}) {
		return p
	}

	// 3b. The signature file, written from the registry.
	//
	// Removed rather than emptied when there is none: NeoMutt appends whatever the file holds,
	// so an empty one that still exists is a blank line and a "-- " separator on every message
	// the account sends, which reads as a signature somebody forgot to finish.
	if !run("write "+a.SigFile(c), func() (string, error) {
		if strings.TrimSpace(a.Signature) == "" {
			if err := os.Remove(a.SigFile(c)); err != nil {
				if os.IsNotExist(err) {
					return "none", nil
				}
				return "", err
			}
			return "removed", nil
		}
		if err := os.MkdirAll(filepath.Dir(a.SigFile(c)), 0o700); err != nil {
			return "", err
		}
		body := signatureBody(a)
		if same, err := unchanged(a.SigFile(c), body); err == nil && same {
			return "already up to date", nil
		}
		return "written", config.WriteAtomic(a.SigFile(c), []byte(body), 0o600)
	}) {
		return p
	}

	// 4. The mbsync block, between markers in a file mailforge does not own.
	if !run("update "+c.MbsyncRC, func() (string, error) {
		res, err := edit.Upsert(c.MbsyncRC, a.Address, render.MbsyncBlock(c, a), false)
		return res.Note, err
	}) {
		return p
	}

	// 5. The registry, and then everything that is derived from all of it.
	if !run("record it in "+account.RegistryPath(), func() (string, error) {
		if err := r.Upsert(a); err != nil {
			return "", err
		}
		return "saved", r.Save()
	}) {
		return p
	}

	ApplyIndex(c, r, &p)
	return p
}

// RemoveAccount unwrites an account, and is deliberately narrower than ApplyAccount.
//
// The mail and the password are NOT touched by default. Deleting a record and deleting somebody's
// correspondence are different acts; a program that does the second while being asked for the
// first is a program nobody should run. The caller passes what it means explicitly.
func RemoveAccount(c config.Config, r *account.Registry, a account.Account, alsoPassword, alsoMail bool) Plan {
	var p Plan
	run := func(what string, fn func() (string, error)) {
		s := Step{What: what}
		note, err := fn()
		s.Done, s.Note, s.Err = err == nil, note, err
		p.Steps = append(p.Steps, s)
	}

	run("remove the block from "+c.MbsyncRC, func() (string, error) {
		res, err := edit.Remove(c.MbsyncRC, a.Address, false)
		return res.Note, err
	})
	run("delete "+a.SigFile(c), func() (string, error) {
		if err := os.Remove(a.SigFile(c)); err != nil {
			if os.IsNotExist(err) {
				return "was not there", nil
			}
			return "", err
		}
		return "deleted", nil
	})
	run("delete "+a.RCFile(c), func() (string, error) {
		if err := os.Remove(a.RCFile(c)); err != nil {
			if os.IsNotExist(err) {
				return "was not there", nil
			}
			return "", err
		}
		return "deleted", nil
	})
	run("drop it from "+account.RegistryPath(), func() (string, error) {
		if !r.Remove(a.Address) {
			return "was not listed", r.Save()
		}
		return "saved", r.Save()
	})

	if alsoPassword {
		run("remove "+a.PassEntry(c)+" from pass", func() (string, error) {
			if !store.Exists(a.PassEntry(c)) {
				return "was not there", nil
			}
			return "removed", store.Remove(a.PassEntry(c))
		})
	}
	if alsoMail {
		run("delete "+a.Maildir(c), func() (string, error) {
			if err := os.RemoveAll(a.Maildir(c)); err != nil {
				return "", err
			}
			return "deleted", nil
		})
	}

	ApplyIndex(c, r, &p)
	return p
}

// applyIndex rewrites everything derived from the whole registry: the index and the one line
// inside the user's own neomuttrc.
//
// Separate from the per-account work because it is a function of ALL accounts — adding one changes
// the sidebar of every other, and a version of this that only appended would drift the moment an
// account was removed.
// ApplyIndex is exported because a reorder or a change of default touches nothing about an
// account itself and everything about the index — the interface needs to run just this half.
func ApplyIndex(c config.Config, r *account.Registry, p *Plan) {
	run := func(what string, fn func() (string, error)) {
		s := Step{What: what}
		note, err := fn()
		s.Done, s.Note, s.Err = err == nil, note, err
		p.Steps = append(p.Steps, s)
	}

	idx := filepath.Join(c.NeomuttDir, "mailforge", "accounts.rc")
	run("write "+idx, func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(idx), 0o700); err != nil {
			return "", err
		}
		body := render.AccountsRC(c, r.Accounts)
		if same, err := unchanged(idx, body); err == nil && same {
			return "already up to date", nil
		}
		return "written", config.WriteAtomic(idx, []byte(body), 0o600)
	})

	ApplyKeys(c, p)
	ApplyOptions(c, p)
	ApplyTimer(c, p)

	rc := filepath.Join(c.NeomuttDir, "neomuttrc")
	run("point "+rc+" at it", func() (string, error) {
		res, err := edit.Upsert(rc, "", render.NeomuttInclude(c), false)
		return res.Note, err
	})
}

// ApplyKeys writes keys.rc and points neomuttrc at it.
//
// Its own function because it is a function of the CONFIG alone — no account touches it — and the
// Keys tab has to be able to run just this, so that changing a binding takes effect without also
// re-rendering every account. The file goes down before the source line goes in: a source line to
// a file that is not there is an error banner on every start, for a file that would have been
// written a moment later anyway.
func ApplyKeys(c config.Config, p *Plan) {
	run := func(what string, fn func() (string, error)) {
		s := Step{What: what}
		note, err := fn()
		s.Done, s.Note, s.Err = err == nil, note, err
		p.Steps = append(p.Steps, s)
	}

	kf := filepath.Join(c.NeomuttDir, "mailforge", "keys.rc")
	run("write "+kf, func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(kf), 0o700); err != nil {
			return "", err
		}
		body := render.KeysRC(c)
		if same, err := unchanged(kf, body); err == nil && same {
			return "already up to date", nil
		}
		return "written", config.WriteAtomic(kf, []byte(body), 0o600)
	})

	rc := filepath.Join(c.NeomuttDir, "neomuttrc")
	run("point "+rc+" at the keys", func() (string, error) {
		// TOP, so the user's own bindings.rc is read afterwards and wins.
		res, err := edit.UpsertTop(rc, "keys", render.NeomuttKeysInclude(c), false)
		return res.Note, err
	})
}

// ApplyOptions writes options.rc and points neomuttrc at it.
//
// A twin of ApplyKeys, and separate from it for the same reason: it is a function of the CONFIG
// alone, so the Settings tab can run just this and have a changed editor take effect without
// re-rendering every account. The file goes down before the source line goes in.
func ApplyOptions(c config.Config, p *Plan) {
	of := filepath.Join(c.NeomuttDir, "mailforge", "options.rc")
	Write(p, "write "+of, func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(of), 0o700); err != nil {
			return "", err
		}
		body := render.OptionsRC(c)
		if same, err := unchanged(of, body); err == nil && same {
			return "already up to date", nil
		}
		return "written", config.WriteAtomic(of, []byte(body), 0o600)
	})

	rc := filepath.Join(c.NeomuttDir, "neomuttrc")
	Write(p, "point "+rc+" at the options", func() (string, error) {
		// TOP, so the user's own settings.rc is read afterwards and wins.
		res, err := edit.UpsertTop(rc, "options", render.NeomuttOptionsInclude(c), false)
		return res.Note, err
	})
}

// Write runs one step and records it. Exported because the two Apply* functions above and the
// signature editor all need the same three lines, and three copies of them is how the plan starts
// reporting different things depending on which path wrote the file.
func Write(p *Plan, what string, fn func() (string, error)) {
	s := Step{What: what}
	note, err := fn()
	s.Done, s.Note, s.Err = err == nil, note, err
	p.Steps = append(p.Steps, s)
}

// unchanged reports whether a file already holds exactly this. Used to keep apply inert when
// nothing moved, which is what makes it safe to run as often as you like.
// signatureBody is what goes in the file: the text, ending in exactly one newline.
//
// NeoMutt appends the file verbatim, so trailing blank lines the editor left behind become blank
// lines at the bottom of every message.
func signatureBody(a account.Account) string {
	return strings.TrimRight(a.Signature, "\n \t") + "\n"
}

func unchanged(path, body string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return string(b) == body, nil
}

// Describe renders a plan as lines for the interface.
func Describe(p Plan) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		switch {
		case s.Err != nil:
			out = append(out, fmt.Sprintf("✕ %s — %v", s.What, s.Err))
		case s.Done:
			note := s.Note
			if note != "" {
				note = " — " + note
			}
			out = append(out, fmt.Sprintf("✓ %s%s", s.What, note))
		default:
			out = append(out, "· "+s.What)
		}
	}
	return out
}
