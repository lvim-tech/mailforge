package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/provider"
	"github.com/lvim-tech/mailforge/internal/setup"
	"github.com/lvim-tech/mailforge/internal/sys"
	"github.com/lvim-tech/mailforge/internal/verify"
)

// mode is which screen of this tab is showing. The wizard is a sequence of them rather than one
// form with everything on it, because each step has a different way of failing and the user should
// only ever be sent back to the step that was wrong.
type mode int

const (
	mList mode = iota
	mIdentity
	mServers
	mPassword
	mVerify
	mFolders
	mApply
	mConfirmDelete
)

type accountsView struct {
	cfg config.Config
	reg account.Registry

	mode mode
	sel  int
	err  string

	// wizard state
	editing bool // true when the wizard is changing an account that already exists
	// stored is true when pass already holds an entry under this address, whether or not
	// mailforge put it there. It is what lets the password step be skippable for an account
	// being added: somebody who kept their passwords in pass long before this program existed
	// should not have to retype one to be allowed past a screen.
	stored   bool
	draft    account.Account
	field    int
	fAddress input
	fReal    input
	fLabel   input
	fIHost   input
	fIPort   input
	fISec    input
	fIUser   input
	fSHost   input
	fSPort   input
	fSSec    input
	fSUser   input
	fSent    input
	fDrafts  input
	fTrash   input
	fSpam    input
	fArchive input
	// fPassPath is the pass entry the password goes to, shown and editable rather than derived
	// silently. The store belongs to the person, not to mailforge: it may be filed by domain,
	// it may already hold this password under a name mailforge would never have chosen, and the
	// only way to be sure the right file is being written is to put the name on the screen next
	// to the field that writes it.
	fPassPath input
	pass      *secretInput
	// passDup are the other entries that also hold a password for this account, found when the
	// step was opened. Non-nil means a choice is owed: two encrypted copies of one secret, and
	// nothing on disk saying which is live.
	passDup []string
	// kfBusy is set while keyforge has the terminal, so the step does not act on stale keys.
	kfBusy bool

	source   string // where the server guess came from
	note     string // provider warning, e.g. app passwords
	imapRes  verify.Result
	smtpRes  verify.Result
	busy     string // what is being waited for, shown as a line rather than a spinner
	plan     setup.Plan
	delPass  bool
	delMail  bool
	delFocus int
}

func newAccountsView(c config.Config) *accountsView {
	v := &accountsView{cfg: c}
	v.resetForms()
	return v
}

func (v *accountsView) resetForms() {
	// The hints of the OPTIONAL fields name what happens when they are left empty, and that is
	// the truth: an empty port really does become 993. The REQUIRED ones must not do the same,
	// because there "993" would be a lie about a field that stops the wizard — so they say so,
	// and the example is behind the word rather than in front of it.
	v.fAddress = newInput("address", "required — you@example.com")
	v.fReal = newInput("real name", v.cfg.RealName)
	v.fLabel = newInput("label", "left empty, derived from the address")
	v.fIHost = newInput("IMAP host", "required — imap.example.com")
	v.fIPort = newInput("IMAP port", "empty = 993")
	v.fISec = newInput("IMAP security", "empty = tls")
	v.fIUser = newInput("IMAP login", "empty = the address")
	v.fSHost = newInput("SMTP host", "required — smtp.example.com")
	v.fSPort = newInput("SMTP port", "empty = 465")
	v.fSSec = newInput("SMTP security", "empty = tls")
	v.fSUser = newInput("SMTP login", "empty = the address")
	v.fSent = newInput("Sent", "discovered from the server")
	v.fDrafts = newInput("Drafts", "")
	v.fTrash = newInput("Trash", "")
	v.fSpam = newInput("Spam", "")
	v.fArchive = newInput("Archive", "")
	v.fPassPath = newInput("pass entry", "where the password is read from and written to")
	v.passDup, v.kfBusy = nil, false
	if v.pass != nil {
		v.pass.close()
	}
	v.pass = newSecretInput("password", "typed into locked memory, stored in pass")
}

func (v *accountsView) Title() string { return "Accounts" }

func (v *accountsView) capturesInput() bool { return v.mode != mList && v.mode != mConfirmDelete }

func (v *accountsView) Init() tea.Cmd { return v.load() }

type accountsLoaded struct {
	reg account.Registry
	err error
}

func (v *accountsView) load() tea.Cmd {
	return func() tea.Msg {
		r, err := account.Load()
		return accountsLoaded{reg: r, err: err}
	}
}

func (v *accountsView) current() (account.Account, bool) {
	if v.sel < 0 || v.sel >= len(v.reg.Accounts) {
		return account.Account{}, false
	}
	return v.reg.Accounts[v.sel], true
}

// screenID lets the shell notice a wizard step change and drop a status line that was about the
// step before it. See app.go.
func (v *accountsView) screenID() int { return int(v.mode) }

func (v *accountsView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case accountsLoaded:
		v.reg = msg.reg
		if msg.err != nil {
			v.err = msg.err.Error()
		} else {
			v.err = ""
		}
		if v.sel >= len(v.reg.Accounts) {
			v.sel = maxi(len(v.reg.Accounts)-1, 0)
		}
		return v, nil

	case reloadMsg:
		if v.mode == mList {
			return v, v.load()
		}
		return v, nil

	case discovered:
		v.busy = ""
		v.applyGuess(provider.Guess(msg))
		return v, nil

	case verified:
		v.busy = ""
		v.imapRes, v.smtpRes = msg.imap, msg.smtp
		if msg.imap.OK && msg.smtp.OK {
			v.draft.Folders = msg.imap.Folders
			v.draft.Verified = time.Now()
			v.loadFolderFields()
			v.mode = mFolders
		}
		return v, nil

	case reverified:
		return v, v.recordVerified(msg)

	case applied:
		v.busy = ""
		v.plan = setup.Plan(msg)
		return v, v.load()

	case signatureEdited:
		return v, v.applySignature(msg)

	case passwordGenerated:
		return v, v.applyGenerated(msg)

	case tea.KeyMsg:
		return v.key(msg)
	}
	return v, nil
}

func (v *accountsView) key(msg tea.KeyMsg) (view, tea.Cmd) {
	switch v.mode {
	case mList:
		return v.keyList(msg)
	case mConfirmDelete:
		return v.keyDelete(msg)
	case mApply:
		if msg.String() == "esc" || msg.String() == "enter" {
			v.mode = mList
			v.plan = setup.Plan{}
			return v, v.load()
		}
		return v, nil
	default:
		return v.keyWizard(msg)
	}
}

func (v *accountsView) keyList(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if v.sel < len(v.reg.Accounts)-1 {
			v.sel++
		}
	case "k", "up":
		if v.sel > 0 {
			v.sel--
		}
	case "g":
		v.sel = 0
	case "G":
		v.sel = maxi(len(v.reg.Accounts)-1, 0)
	case "a":
		v.startAdd()
	case "e":
		if a, ok := v.current(); ok {
			v.startEdit(a)
		}
	case "d":
		if _, ok := v.current(); ok {
			v.mode, v.delPass, v.delMail, v.delFocus = mConfirmDelete, false, false, 0
		}
	case "D":
		if a, ok := v.current(); ok {
			v.reg.SetDefault(a.Address)
			if err := v.reg.Save(); err != nil {
				return v, failure("%v", err)
			}
			return v, tea.Batch(v.applyIndexOnly(), status("%s is the default account", a.Address))
		}
	case "J":
		if a, ok := v.current(); ok && v.reg.Move(a.Address, 1) {
			if err := v.reg.Save(); err != nil {
				return v, failure("%v", err)
			}
			v.sel++
			return v, v.applyIndexOnly()
		}
	case "K":
		if a, ok := v.current(); ok && v.reg.Move(a.Address, -1) {
			if err := v.reg.Save(); err != nil {
				return v, failure("%v", err)
			}
			v.sel--
			return v, v.applyIndexOnly()
		}
	case "s":
		if a, ok := v.current(); ok {
			return v, v.editSignature(a)
		}
	case "v":
		if a, ok := v.current(); ok {
			return v, v.reverify(a)
		}
	case "r":
		return v, v.load()
	}
	return v, nil
}

// reverify checks an existing account without changing anything, reading its password back out of
// pass into locked memory for the attempt.
//
// Its result is its OWN message type. Sharing `verified` with the wizard meant a check started from
// the list ended in the wizard's folder step, applied to whatever draft happened to be left in the
// view — a different account's, or the zero one.
func (v *accountsView) reverify(a account.Account) tea.Cmd {
	v.busy = "checking " + a.Address
	cfg := v.cfg
	return func() tea.Msg {
		sec, err := passwordOf(cfg, a)
		if err != nil {
			return reverified{
				address: a.Address,
				imap:    verifyFail(err.Error()),
				smtp:    verifyFail(err.Error()),
			}
		}
		defer sec.Close()
		res := runChecks(cfg, a, sec)
		return reverified{address: a.Address, imap: res.imap, smtp: res.smtp}
	}
}

// recordVerified saves what a re-check proved. The saving is the point: without it the doctor goes
// on reporting the account as verified long ago however often the user re-checks it.
func (v *accountsView) recordVerified(msg reverified) tea.Cmd {
	v.busy = ""
	if !msg.imap.OK || !msg.smtp.OK {
		which, detail := "IMAP", msg.imap.Detail
		if msg.imap.OK {
			which, detail = "SMTP", msg.smtp.Detail
		}
		return failure("%s: %s failed at %s — %s", msg.address, which,
			pickStage(msg.imap, msg.smtp), detail)
	}
	for i := range v.reg.Accounts {
		if v.reg.Accounts[i].Address != msg.address {
			continue
		}
		v.reg.Accounts[i].Verified = time.Now()
		if msg.imap.Folders.Any() {
			v.reg.Accounts[i].Folders = msg.imap.Folders
		}
		if err := v.reg.Save(); err != nil {
			return failure("%v", err)
		}
		return status("%s verified ok", msg.address)
	}
	// The account was removed while the check was in flight; there is nothing to record.
	return status("%s verified ok, but it is no longer in the registry", msg.address)
}

func pickStage(imap, smtp verify.Result) verify.Stage {
	if !imap.OK {
		return imap.Reached
	}
	return smtp.Reached
}

func (v *accountsView) keyDelete(msg tea.KeyMsg) (view, tea.Cmd) {
	a, ok := v.current()
	if !ok {
		v.mode = mList
		return v, nil
	}
	switch msg.String() {
	case "esc", "n":
		v.mode = mList
	case "tab", "j", "down":
		v.delFocus = (v.delFocus + 1) % 2
	case "k", "up":
		v.delFocus = (v.delFocus + 1) % 2
	case " ":
		if v.delFocus == 0 {
			v.delPass = !v.delPass
		} else {
			v.delMail = !v.delMail
		}
	case "enter", "y":
		cfg, reg := v.cfg, v.reg.Clone()
		pass, mail := v.delPass, v.delMail
		v.busy = "removing " + a.Address
		v.mode = mApply
		return v, func() tea.Msg {
			return applied(setup.RemoveAccount(cfg, &reg, a, pass, mail))
		}
	}
	return v, nil
}

// applyIndexOnly re-renders what depends on the whole registry after a reorder or a default
// change: the per-account files are untouched, but every one of their sidebar positions moved.
func (v *accountsView) applyIndexOnly() tea.Cmd {
	cfg, reg := v.cfg, v.reg.Clone()
	return func() tea.Msg {
		var p setup.Plan
		setup.ApplyIndex(cfg, &reg, &p)
		setup.ApplyConfig(cfg, &p)
		return applied(p)
	}
}

func (v *accountsView) Footer() string {
	switch v.mode {
	case mList:
		f := []string{hint("j/k", "move"), hint("a", "add")}
		if len(v.reg.Accounts) > 0 {
			f = append(f,
				hint("e", "edit"), hint("s", "signature"), hint("d", "delete"),
				hint("v", "verify"), hint("D", "make default"), hint("J/K", "reorder"),
				hint("r", "reload"))
		}
		return joinHints(f...)
	case mConfirmDelete:
		return joinHints(hint("space", "toggle"), hint("enter", "delete"), hint("esc", "cancel"))
	case mApply:
		return joinHints(hint("enter", "back"))
	case mVerify:
		return joinHints(hint("esc", "back"))
	case mPassword:
		// The generate key is named for what it will DO, which is not the same word in the two
		// states: on an empty entry it makes the first password, on an occupied one it replaces
		// a password that already works. A footer that said "generate" for both would be
		// offering, on the second, something closer to "overwrite".
		what := "generate in " + sys.KeyforgeBin
		if v.stored {
			what = "change in " + sys.KeyforgeBin
		}
		return joinHints(hint("tab", "field"), hint("ctrl+o", what),
			hint("enter", "continue"), hint("esc", "back"))
	default:
		return joinHints(hint("tab", "next field"), hint("enter", "continue"), hint("esc", "back"))
	}
}

func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func atoiOr(s string, dflt int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return dflt
}

// discovered, verified and applied carry the results of the three things that happen off the draw
// path. Each is its own type so the update switch reads as a list of outcomes rather than a set of
// booleans nobody can keep straight.
type discovered provider.Guess

type verified struct {
	imap verify.Result
	smtp verify.Result
}

// reverified is the same outcome reached from the LIST, where it means "record this", not "go on
// to the next wizard step". It carries the address because by the time it arrives the selection may
// have moved to another account.
type reverified struct {
	address string
	imap    verify.Result
	smtp    verify.Result
}

type applied setup.Plan

func runChecks(cfg config.Config, a account.Account, sec *sys.Secret) verified {
	to := time.Duration(cfg.VerifyTimeoutSec) * time.Second
	// Sequential, not concurrent: both read from the same Secret, and one buffer with two
	// readers is a race for the sake of a second saved.
	i := verify.IMAP(a.IMAP, a.Address, sec, to)
	s := verify.SMTP(a.SMTP, a.Address, sec, to)
	return verified{imap: i, smtp: s}
}

func discoverCmd(address string, allowISPDB bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return discovered(provider.Discover(ctx, address, allowISPDB))
	}
}
