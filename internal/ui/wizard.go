package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/provider"
	"github.com/lvim-tech/mailforge/internal/setup"
	"github.com/lvim-tech/mailforge/internal/store"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// The wizard: one step per screen, nothing touched on disk until the last one.
//
// The steps are separate screens rather than one long form because they fail differently. A wrong
// password must not send anybody back to retype a hostname, and a hostname that does not resolve
// must not look like a rejected password — so each stage owns its own retry.

func (v *accountsView) startAdd() {
	v.resetForms()
	v.editing = false
	v.stored = false
	v.draft = account.Account{Auth: "password"}
	v.fReal.set(v.cfg.RealName)
	v.source, v.note, v.err = "", "", ""
	v.imapRes, v.smtpRes = verifyZero(), verifyZero()
	v.mode, v.field = mIdentity, 0
}

func (v *accountsView) startEdit(a account.Account) {
	v.resetForms()
	v.editing = true
	v.stored = false
	v.draft = a
	// SEEDED, not set: an existing value behaves like selected text, so the first thing typed
	// over it replaces it. Setting made the fields append-only — changing a host meant deleting
	// twenty characters first, and nothing on the screen said that ctrl+u would do it in one.
	// Seeding keeps the other case too: an arrow or a backspace first means "correct this", and
	// the value survives.
	v.fAddress.seed(a.Address)
	v.fReal.seed(a.RealName)
	v.fLabel.seed(a.Label)
	v.loadServerFields()
	v.loadFolderFields()
	v.source = "as recorded"
	v.note, v.err = "", ""
	v.mode, v.field = mIdentity, 0
}

// loadServerFields and loadFolderFields fill a form from the draft, and SEED rather than set —
// every value they put there is one the user may want to replace outright. See startEdit.
func (v *accountsView) loadServerFields() {
	v.fIHost.seed(v.draft.IMAP.Host)
	v.fIPort.seed(portString(v.draft.IMAP.Port))
	v.fISec.seed(string(v.draft.IMAP.Security))
	v.fIUser.seed(v.draft.IMAP.LoginUser)
	v.fSHost.seed(v.draft.SMTP.Host)
	v.fSPort.seed(portString(v.draft.SMTP.Port))
	v.fSSec.seed(string(v.draft.SMTP.Security))
	v.fSUser.seed(v.draft.SMTP.LoginUser)
}

func (v *accountsView) loadFolderFields() {
	v.fSent.seed(v.draft.Folders.Sent)
	v.fDrafts.seed(v.draft.Folders.Drafts)
	v.fTrash.seed(v.draft.Folders.Trash)
	v.fSpam.seed(v.draft.Folders.Spam)
	v.fArchive.seed(v.draft.Folders.Archive)
}

func portString(p int) string {
	if p <= 0 {
		return ""
	}
	return strconv.Itoa(p)
}

// applyGuess fills the server form from discovery, leaving anything the user already typed alone.
func (v *accountsView) applyGuess(g provider.Guess) {
	v.source, v.note = g.Source, g.Note
	if g.Quirks.NoRecord || len(g.Quirks.SyncExclude) > 0 {
		v.draft.Quirks = g.Quirks
	}
	if v.fIHost.empty() && g.IMAP.Host != "" {
		v.fIHost.set(g.IMAP.Host)
		v.fIPort.set(portString(g.IMAP.Port))
		v.fISec.set(string(g.IMAP.Security))
		if g.IMAP.LoginUser != "" {
			v.fIUser.set(g.IMAP.LoginUser)
		}
	}
	if v.fSHost.empty() && g.SMTP.Host != "" {
		v.fSHost.set(g.SMTP.Host)
		v.fSPort.set(portString(g.SMTP.Port))
		v.fSSec.set(string(g.SMTP.Security))
		if g.SMTP.LoginUser != "" {
			v.fSUser.set(g.SMTP.LoginUser)
		}
	}
}

// fields returns the inputs of the current step, in tab order.
func (v *accountsView) fields() []*input {
	switch v.mode {
	case mIdentity:
		return []*input{&v.fAddress, &v.fReal, &v.fLabel}
	case mServers:
		return []*input{&v.fIHost, &v.fIPort, &v.fISec, &v.fIUser, &v.fSHost, &v.fSPort, &v.fSSec, &v.fSUser}
	case mFolders:
		return []*input{&v.fSent, &v.fDrafts, &v.fTrash, &v.fSpam, &v.fArchive}
	}
	return nil
}

func (v *accountsView) keyWizard(msg tea.KeyMsg) (view, tea.Cmd) {
	// Enter is REFUSED while a lookup is in flight, and only Enter.
	//
	// Discovery takes up to eight seconds. Pressing enter inside that window validated fields the
	// program was in the middle of filling, so the answer was "the IMAP host is required" about a
	// host that arrived a second later — which reads as "it did not find anything" for a lookup
	// that succeeded. Typing is still allowed throughout: somebody who would rather enter the
	// servers by hand than wait must not be blocked, and applyGuess never overwrites a field they
	// have already filled.
	if v.busy != "" && v.mode == mServers && msg.String() == "enter" {
		return v, status("still looking up the servers — a moment")
	}

	// The password step owns its own key handling: what is typed there goes into locked memory
	// and must never pass through an ordinary input.
	if v.mode == mPassword {
		switch msg.String() {
		case "esc":
			v.mode = mServers
			return v, nil
		case "enter":
			if v.pass.empty() && !v.editing && !v.stored {
				return v, failure("a password is required")
			}
			if v.pass.empty() {
				// Nothing typed, and there is a password to fall back on: either this account
				// is being edited, or its entry was already in pass before mailforge knew the
				// address. Verification reads it back out rather than assuming it is right.
				return v, v.verifyStep(true)
			}
			return v, v.verifyStep(false)
		}
		v.pass.update(msg)
		return v, nil
	}

	switch msg.String() {
	case "esc":
		switch v.mode {
		case mIdentity:
			v.mode = mList
		case mServers:
			v.mode = mIdentity
		case mVerify:
			v.mode = mPassword
		case mFolders:
			v.mode = mServers
		}
		v.field = 0
		return v, nil

	case "tab", "down":
		if f := v.fields(); len(f) > 0 {
			v.field = (v.field + 1) % len(f)
		}
		return v, nil
	case "shift+tab", "up":
		if f := v.fields(); len(f) > 0 {
			v.field = (v.field - 1 + len(f)) % len(f)
		}
		return v, nil

	case "enter":
		return v.advance()
	}

	if f := v.fields(); v.field < len(f) {
		f[v.field].update(msg)
	}
	return v, nil
}

// advance moves to the next step, validating what this one collected.
func (v *accountsView) advance() (view, tea.Cmd) {
	switch v.mode {
	case mIdentity:
		addr := strings.TrimSpace(v.fAddress.String())
		if addr == "" || !strings.Contains(addr, "@") {
			return v, failure("an address is required, with an @ in it")
		}
		if !v.editing {
			if _, exists := v.reg.Find(addr); exists {
				return v, failure("%s is already set up — use [e] to change it", addr)
			}
		}
		v.draft.Address = addr
		v.draft.RealName = strings.TrimSpace(v.fReal.String())
		v.draft.Label = strings.TrimSpace(v.fLabel.String())
		v.mode, v.field = mServers, 0
		if v.fIHost.empty() && v.fSHost.empty() {
			v.busy = "looking up " + addr
			return v, discoverCmd(addr, v.cfg.ISPDB)
		}
		return v, nil

	case mServers:
		if err := v.collectServers(); err != nil {
			return v, failure("%v", err)
		}
		// Asked here, not at startAdd: the entry name is derived from the address, and the
		// address is not settled until the identity step has been left.
		v.stored = store.Exists(v.draft.PassEntry(v.cfg))
		v.mode, v.field = mPassword, 0
		return v, nil

	case mFolders:
		v.collectFolders()
		return v, v.applyStep()
	}
	return v, nil
}

// collectFolders takes the five names off the form and CHANGES ONLY THOSE.
//
// It assigns field by field instead of building a Folders literal, because the struct carries one
// the form has no input for: Delim, the server's hierarchy separator, learned during verification.
// A literal reset it to empty on the last step of every wizard run, and the damage was invisible
// until mail was filed: without the delimiter, LocalFolder cannot translate "INBOX.Sent" into the
// directory mbsync stores it in, so NeoMutt was pointed at "+INBOX.Sent" — a path that exists on
// the server and nowhere on this disk — while mbsync refused the same name locally as ambiguous.
// The account looked configured and quietly filed nothing.
func (v *accountsView) collectFolders() {
	v.draft.Folders.Sent = strings.TrimSpace(v.fSent.String())
	v.draft.Folders.Drafts = strings.TrimSpace(v.fDrafts.String())
	v.draft.Folders.Trash = strings.TrimSpace(v.fTrash.String())
	v.draft.Folders.Spam = strings.TrimSpace(v.fSpam.String())
	v.draft.Folders.Archive = strings.TrimSpace(v.fArchive.String())
}

func (v *accountsView) collectServers() error {
	sec := func(s string) (account.Security, error) {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "tls", "ssl", "":
			return account.TLS, nil
		case "starttls":
			return account.STARTTLS, nil
		}
		return "", fmt.Errorf("security must be tls or starttls, not %q", s)
	}
	is, err := sec(v.fISec.String())
	if err != nil {
		return err
	}
	ss, err := sec(v.fSSec.String())
	if err != nil {
		return err
	}
	v.draft.IMAP = account.Server{
		Host:      strings.TrimSpace(v.fIHost.String()),
		Port:      atoiOr(v.fIPort.String(), defaultPort(is, true)),
		Security:  is,
		LoginUser: strings.TrimSpace(v.fIUser.String()),
	}
	v.draft.SMTP = account.Server{
		Host:      strings.TrimSpace(v.fSHost.String()),
		Port:      atoiOr(v.fSPort.String(), defaultPort(ss, false)),
		Security:  ss,
		LoginUser: strings.TrimSpace(v.fSUser.String()),
	}
	return v.draft.Validate()
}

func defaultPort(s account.Security, imap bool) int {
	switch {
	case imap && s == account.TLS:
		return 993
	case imap:
		return 143
	case s == account.TLS:
		return 465
	default:
		return 587
	}
}

// verifyStep proves the account before anything is written. useStored says the user did not retype
// the password, so it comes back out of pass for the attempt.
func (v *accountsView) verifyStep(useStored bool) tea.Cmd {
	v.mode = mVerify
	v.busy = "checking " + v.draft.Address
	cfg, draft := v.cfg, v.draft
	if useStored {
		return func() tea.Msg {
			sec, err := passwordOf(cfg, draft)
			if err != nil {
				msg := "the stored password could not be read: " + err.Error()
				return verified{imap: verifyFail(msg), smtp: verifyFail(msg)}
			}
			defer sec.Close()
			return runChecks(cfg, draft, sec)
		}
	}
	sec := v.pass.secret()
	return func() tea.Msg { return runChecks(cfg, draft, sec) }
}

// applyStep writes everything, in the order that survives an interruption.
func (v *accountsView) applyStep() tea.Cmd {
	v.mode = mApply
	v.busy = "writing"
	cfg, reg, draft := v.cfg, v.reg, v.draft
	var sec *sys.Secret
	if !v.pass.empty() {
		sec = v.pass.secret()
	}
	return func() tea.Msg {
		return applied(setup.ApplyAccount(cfg, &reg, draft, sec))
	}
}

// passwordOf reads an account's stored password back into locked memory.
func passwordOf(c config.Config, a account.Account) (*sys.Secret, error) {
	return store.Reveal(a.PassEntry(c))
}
