package ui

import (
	"errors"
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
		// While keyforge owns the terminal there is nothing here to type into, and keys that
		// arrived before the handover must not be replayed against a field that is about to be
		// overwritten by the answer.
		if v.kfBusy {
			return v, nil
		}
		switch msg.String() {
		case "esc":
			v.mode = mServers
			return v, nil
		case "tab", "down", "up", "shift+tab":
			v.field = 1 - v.field
			return v, nil
		case "ctrl+o":
			return v, v.generateStep()
		case "enter":
			if v.field == 0 {
				// The path field is the first of two, so Enter moves on rather than
				// submitting: a form that saves from its first field surprises everyone who
				// expected to reach the second.
				if err := v.checkPassPath(); err != nil {
					return v, failure("%v", err)
				}
				v.field = 1
				return v, nil
			}
			if err := v.checkPassPath(); err != nil {
				return v, failure("%v", err)
			}
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
		if v.field == 0 {
			v.fPassPath.update(msg)
			// The entry named here decides whether Enter may pass with nothing typed, so the
			// answer is re-asked as it is edited rather than once when the step opened.
			v.refreshPassStored()
			return v, nil
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
		v.openPasswordStep()
		return v, nil

	case mFolders:
		v.collectFolders()
		return v, v.applyStep()
	}
	return v, nil
}

// openPasswordStep works out which pass entry this account's password belongs in, and says so.
//
// The search covers BOTH layouts, so a store filed by domain is found without anything in it being
// moved or copied. What it must never do is pick quietly when both are occupied: two encrypted
// files, one password, and only their owner knows which is current. That case is recorded in
// passDup and put on the screen as a choice.
func (v *accountsView) openPasswordStep() {
	cands := v.draft.PassCandidates(v.cfg)
	v.passDup = store.Duplicated(cands)
	v.fPassPath.set(store.Resolve(cands))
	v.refreshPassStored()
	v.mode, v.field = mPassword, 0
}

// refreshPassStored re-asks whether the entry NOW NAMED in the field holds anything.
//
// Called on every keystroke in the path field, because the answer is what the step's whole
// behaviour hangs on — whether Enter may pass with nothing typed, and whether the action offered
// is "generate" or "change". A stale answer here is the difference between reusing a password and
// being asked for one that already exists.
func (v *accountsView) refreshPassStored() {
	v.stored = false
	if name := strings.TrimSpace(v.fPassPath.String()); name != "" {
		v.stored = store.Exists(name)
	}
}

// passEntryChosen is what the path field currently names, cleaned the way the store cleans it.
func (v *accountsView) passEntryChosen() string {
	return strings.Trim(strings.TrimSpace(v.fPassPath.String()), "/")
}

// checkPassPath refuses a name the store could not honour, at the field rather than at apply time.
//
// store.ValidName is the same check the writer runs. Running it here as well is not duplication:
// it is the difference between "that is not a usable entry name" said next to the field that is
// wrong, and a failed step three screens later with the wizard's work already done.
func (v *accountsView) checkPassPath() error {
	name := v.passEntryChosen()
	if name == "" {
		return errors.New("a pass entry is required — it is where the password is read from")
	}
	return store.ValidName(name)
}

// recordPassChoice puts the entry named on screen into the draft, when it is worth recording.
//
// Not always, because accounts.json is read by people: an explicit pass_entry on every account
// that simply agrees with pass_layout is noise, and noise is what stops a file being read at all.
// It IS recorded when the name differs from what the layout derives — otherwise a later change to
// pass_layout would silently relocate an account that had been pointed somewhere deliberately —
// and whenever the store held more than one candidate, because there the name is a DECISION and a
// decision that is not written down gets made again, differently, by the next code to look.
func (v *accountsView) recordPassChoice() {
	name := v.passEntryChosen()
	if name == "" {
		return
	}
	if name != v.draft.PassEntryIn(v.cfg, v.cfg.PassLayout) || len(v.passDup) > 1 {
		v.draft.PassOverride = name
		return
	}
	v.draft.PassOverride = ""
}

// passwordGenerated carries keyforge's answer back into the wizard.
//
// The value is NOT in this message. It went straight from the pipe into the field's locked buffer;
// what travels through bubbletea's ordinary message queue is only whether something arrived.
type passwordGenerated struct {
	got bool
	err error
}

// generateStep hands the terminal to keyforge and takes back a password.
//
// mailforge asks rather than generates. The rules for what a password may contain live in
// keyforge, on a screen where they can be seen and changed, and this is the moment they are worth
// consulting — so the person picks the shape they want there, presses [S], and lands back here
// with the field filled.
func (v *accountsView) generateStep() tea.Cmd {
	cmd, hb, err := sys.Keyforge()
	if err != nil {
		return failure("%v", err)
	}
	v.kfBusy = true
	buf := v.pass.secret()
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		// Into runs even when the command failed: it is what closes this side of the pipe, and
		// a descriptor leaked on the error path is still a leak.
		intoErr := hb.Into(buf)
		if runErr != nil {
			return passwordGenerated{err: runErr}
		}
		if intoErr != nil {
			return passwordGenerated{err: intoErr}
		}
		return passwordGenerated{got: !buf.Empty()}
	})
}

// applyGenerated is what the wizard does once keyforge has been and gone.
func (v *accountsView) applyGenerated(msg passwordGenerated) tea.Cmd {
	v.kfBusy = false
	if msg.err != nil {
		return failure("%v", msg.err)
	}
	if !msg.got {
		// Quit without [S]. Nothing was written into the buffer, so there is nothing to undo
		// and nothing to apologise for.
		return status("%s closed without returning a password", sys.KeyforgeBin)
	}
	// Straight to the field that submits: a generated password is not one anybody wants to
	// retype, and leaving focus on the path invites an Enter that reads as "discard this".
	v.field = 1
	return status("a password came back from %s — [enter] verifies it", sys.KeyforgeBin)
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
	v.recordPassChoice()
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
	// A SNAPSHOT, not the live buffer. Esc leaves this screen while the check is still running,
	// and the next keystroke in the password field would otherwise edit the bytes the goroutine
	// is streaming into the socket.
	sec, err := v.pass.secret().Snapshot()
	if err != nil {
		msg := "the password could not be prepared: " + err.Error()
		return func() tea.Msg { return verified{imap: verifyFail(msg), smtp: verifyFail(msg)} }
	}
	return func() tea.Msg {
		defer sec.Close()
		return runChecks(cfg, draft, sec)
	}
}

// applyStep writes everything, in the order that survives an interruption.
func (v *accountsView) applyStep() tea.Cmd {
	v.mode = mApply
	v.busy = "writing"
	// Clone, not assign: the apply runs in another goroutine and sorts and rewrites the accounts
	// while this one is still drawing them.
	cfg, reg, draft := v.cfg, v.reg.Clone(), v.draft
	var sec *sys.Secret
	if !v.pass.empty() {
		// Snapshot for the same reason verifyStep does: the write happens off this goroutine.
		s, err := v.pass.secret().Snapshot()
		if err != nil {
			return failure("the password could not be prepared: %v", err)
		}
		sec = s
	}
	return func() tea.Msg {
		if sec != nil {
			defer sec.Close()
		}
		return applied(setup.ApplyAccount(cfg, &reg, draft, sec))
	}
}

// passwordOf reads an account's stored password back into locked memory.
func passwordOf(c config.Config, a account.Account) (*sys.Secret, error) {
	return store.Reveal(a.PassEntry(c))
}
