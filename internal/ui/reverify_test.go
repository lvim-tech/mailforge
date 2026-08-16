package ui

import (
	"testing"
	"time"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/verify"
)

// The bug these tests lock: [v] on the account list produced the SAME message the wizard's verify
// step does, and the handler for it was unconditional. A successful re-check therefore jumped into
// the wizard's folder step carrying whatever draft the view happened to hold — the zero one on a
// fresh session, or the account edited earlier in this one, which the next enter would then
// overwrite with folder names discovered from a different server. The timestamp the re-check
// existed to refresh was never saved either.

func viewWith(t *testing.T, a account.Account) *accountsView {
	t.Helper()
	v := newAccountsView(config.Default())
	v.reg = account.Registry{Accounts: []account.Account{a}}
	v.mode = mList
	return v
}

func okResult() verify.Result {
	return verify.Result{OK: true, Reached: verify.StageDone}
}

func TestReverifyStaysOnTheListAndRecordsTheTime(t *testing.T) {
	a := account.Account{Address: "someone@example.com"}
	v := viewWith(t, a)
	v.busy = "checking " + a.Address

	res := okResult()
	res.Folders = account.Folders{Delim: ".", Sent: "INBOX.Sent"}
	_, cmd := v.Update(reverified{address: a.Address, imap: res, smtp: okResult()})

	if v.mode != mList {
		t.Fatalf("a re-check from the list left the list for mode %v", v.mode)
	}
	if v.busy != "" {
		t.Errorf("busy is still %q — the list would say 'checking' for ever", v.busy)
	}
	if v.reg.Accounts[0].Verified.IsZero() {
		t.Error("the verification time was not recorded; the doctor would go on reporting it stale")
	}
	if got := v.reg.Accounts[0].Folders.Sent; got != "INBOX.Sent" {
		t.Errorf("discovered folders were not kept: Sent = %q", got)
	}
	if cmd == nil {
		t.Error("nothing was reported to the user")
	}
}

// A draft left behind by an earlier edit must not be touched by a re-check of another account.
func TestReverifyLeavesTheDraftAlone(t *testing.T) {
	a := account.Account{Address: "someone@example.com"}
	v := viewWith(t, a)
	v.draft = account.Account{Address: "other@example.com"}

	res := okResult()
	res.Folders = account.Folders{Sent: "Sent"}
	v.Update(reverified{address: a.Address, imap: res, smtp: okResult()})

	if v.draft.Address != "other@example.com" {
		t.Fatalf("the draft was replaced: %q", v.draft.Address)
	}
	if v.draft.Folders.Sent != "" {
		t.Errorf("the other account's folders were written into the draft: %q", v.draft.Folders.Sent)
	}
	if !v.draft.Verified.IsZero() {
		t.Error("the draft was stamped as verified by a check of a different account")
	}
}

// A failed re-check must say so. Silence was the old behaviour and it was indistinguishable from
// success on a screen that shows neither result.
func TestReverifyReportsAFailure(t *testing.T) {
	a := account.Account{Address: "someone@example.com", Verified: time.Time{}}
	v := viewWith(t, a)

	_, cmd := v.Update(reverified{
		address: a.Address,
		imap:    verify.Result{Reached: verify.StageAuth, Detail: "authentication failed"},
		smtp:    okResult(),
	})

	if cmd == nil {
		t.Fatal("a failed re-check reported nothing")
	}
	if !v.reg.Accounts[0].Verified.IsZero() {
		t.Error("a failed check stamped the account as verified")
	}
	msg, ok := cmd().(statusMsg)
	if !ok {
		t.Fatalf("expected a status message, got %T", cmd())
	}
	if !msg.err {
		t.Error("the failure was reported as an ordinary status rather than an error")
	}
}

// The wizard's own message keeps its behaviour: it is the one that advances to the folder step.
func TestVerifiedStillAdvancesTheWizard(t *testing.T) {
	v := newAccountsView(config.Default())
	v.mode = mVerify
	v.draft = account.Account{Address: "someone@example.com"}

	res := okResult()
	res.Folders = account.Folders{Sent: "Sent"}
	v.Update(verified{imap: res, smtp: okResult()})

	if v.mode != mFolders {
		t.Fatalf("the wizard did not reach the folders step: mode %v", v.mode)
	}
	if v.draft.Verified.IsZero() {
		t.Error("the draft was not stamped as verified")
	}
}
