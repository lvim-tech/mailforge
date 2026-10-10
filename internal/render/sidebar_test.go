package render

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/account"
)

// TestSidebarRCFoldsEveryAccountToOneLine is the whole point of the file: twelve accounts of six
// folders is eighty-four lines in a thirty-two column strip, and folded it is twelve.
func TestSidebarRCFoldsEveryAccountToOneLine(t *testing.T) {
	c, hosted, gmail := fixtures()
	c.SidebarCollapse = true

	got := SidebarRC(c, []account.Account{hosted, gmail}, nil, "")

	if !strings.Contains(got, "\nunmailboxes *\n") {
		t.Error("the list does not start by clearing the old one, so a fold would append to it")
	}
	if n := strings.Count(got, "named-mailboxes "); n != 2 {
		t.Errorf("got %d account lines, want 2", n)
	}
	// NOT one mailboxes line: folded means the folders are not registered at all, which is what
	// makes the sidebar short. A line here would show them with no way to hide them.
	if strings.Contains(got, "mailboxes \"/home/u/.local/share/mail/hello@example.com/Sent\"") {
		t.Error("a folded account registered its folders anyway")
	}
}

// TestSidebarRCOpensOnlyWhatTheStateNames checks the half that makes it a fold rather than a
// setting: one account showing its folders while the others stay shut.
func TestSidebarRCOpensOnlyWhatTheStateNames(t *testing.T) {
	c, hosted, gmail := fixtures()
	c.SidebarCollapse = true

	got := SidebarRC(c, []account.Account{hosted, gmail}, map[string]bool{hosted.Address: true}, "")

	if !strings.Contains(got, "/hello@example.com/Sent") {
		t.Error("the open account did not register its folders")
	}
	if strings.Contains(got, "/someone@gmail.com/[Gmail]/Sent Mail") {
		t.Error("a shut account registered its folders")
	}
	// Order, because the sidebar is sorted by registration and nothing else: sidebar_sort is
	// unsorted, so a file in the wrong order IS a sidebar in the wrong order.
	if strings.Index(got, "hello@example.com/INBOX") > strings.Index(got, "someone@gmail.com/INBOX") {
		t.Error("the accounts are registered out of registry order")
	}
}

// TestSidebarRCIgnoresAnAccountThatIsGone: an address can sit in the state after the account was
// removed, and the state is not the registry's keeper.
func TestSidebarRCIgnoresAnAccountThatIsGone(t *testing.T) {
	c, hosted, _ := fixtures()
	c.SidebarCollapse = true

	got := SidebarRC(c, []account.Account{hosted}, map[string]bool{"deleted@example.com": true}, "")

	if strings.Contains(got, "deleted@example.com") {
		t.Error("an address with no account behind it reached the file")
	}
	if n := strings.Count(got, "named-mailboxes "); n != 1 {
		t.Errorf("got %d account lines, want 1", n)
	}
}

// TestAccountsRCHandsTheListOverWhenFolded: two files must not both own the mailbox list. The
// folded one changes on a keypress; accounts.rc changes on an apply, and an apply must not undo a
// fold.
func TestAccountsRCHandsTheListOverWhenFolded(t *testing.T) {
	c, hosted, gmail := fixtures()
	c.SidebarCollapse = true

	accounts := []account.Account{hosted, gmail}
	got := AccountsRC(c, accounts)

	if !strings.Contains(got, "source "+SidebarPath(c)) {
		t.Error("accounts.rc does not source the folded list, so the sidebar would be empty")
	}
	if strings.Contains(got, "named-mailboxes ") {
		t.Error("accounts.rc still registers mailboxes while folded — two owners for one list")
	}
	// The identity hooks are NOT part of the trade: one per account, as always, because they are
	// what makes a change of folder switch who you are sending as.
	for _, a := range accounts {
		if !strings.Contains(got, "folder-hook \""+a.Address+"/*\"") {
			t.Errorf("%s lost its identity hook", a.Address)
		}
	}
	// And one more, LAST: the hook that says an INBOX was opened. Order is the mechanism — both
	// match an INBOX, and the one defined later has the final say.
	hook := "folder-hook \"/INBOX$\" source " + InboxHookPath(c)
	if !strings.Contains(got, hook) {
		t.Error("the INBOX hook is missing, so Enter cannot tell a row from a folder")
	}
	if strings.Index(got, hook) < strings.LastIndex(got, "/*\" source ") {
		t.Error("the INBOX hook is defined before an account hook, which overwrites it")
	}
}

// TestAccountRCRecordsWhichAccountIsOpen covers the line that lets ONE fixed macro fold whichever
// account is being read.
func TestAccountRCRecordsWhichAccountIsOpen(t *testing.T) {
	c, hosted, _ := fixtures()

	if got := AccountRC(c, hosted); strings.Contains(got, "sidebar current") {
		t.Error("the folder-hook reports the open account while folding is off")
	}

	c.SidebarCollapse = true
	got := AccountRC(c, hosted)
	want := "`mailforge sidebar current 'hello@example.com'`"
	if !strings.Contains(got, want) {
		t.Errorf("the account rc does not record the open account\nwant a line with %s", want)
	}
}

// TestKeysRCBindsBothSpellingsOfEnter guards the pair NeoMutt's own tables name. A terminal sends
// \n or \r for Enter, and binding one of them is a key that works on some machines; binding an
// <enter> instead is a bet, because NeoMutt takes an unknown <name> as a literal sequence without
// complaining — the file loads clean and the key does nothing.
func TestKeysRCBindsBothSpellingsOfEnter(t *testing.T) {
	c, _, _ := fixtures()

	if got := KeysRC(c); strings.Contains(got, "sidebar toggle") {
		t.Error("the fold key is bound while folding is off, so Enter stopped opening mail")
	}

	c.SidebarCollapse = true
	got := KeysRC(c)
	for _, want := range []string{`macro index "\n" `, `macro index "\r" `} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s — Enter is \\n on some terminals and \\r on others", want)
		}
	}
	// The pager is deliberately left alone: the macro opens the highlighted row first, and doing
	// that from an open message would walk out of what is being read.
	if strings.Contains(got, "macro pager \"\\n\"") {
		t.Error("the fold key is bound in the pager, where it would change folder mid-message")
	}
	// The open comes FIRST. Without it the key folds whatever account was already open, which on
	// a fresh start is the default one — Enter folding the top of the list wherever the highlight
	// happens to be.
	if !strings.Contains(got, `"<sidebar-open><shell-escape>`) {
		t.Error("the fold macro does not open the highlighted row before folding it")
	}
	// The KEY is not an <enter>: unknown <names> are accepted in silence, so one would have to be
	// right on trust. <enter> inside the macro BODY is a different thing entirely — there it is
	// the keystroke that submits the shell command, which is what it is for.
	for _, bad := range []string{`macro index "<enter>"`, `macro pager "<enter>"`} {
		if strings.Contains(got, bad) {
			t.Error("bound <enter> as a key, which NeoMutt accepts and never delivers")
		}
	}
	// Every fold macro asks for the counts afterwards, or an account folds open with nothing
	// beside its folders until the next check comes round, and the fold looks like it lost them.
	// Per line, not by counting the file: the fetch macros carry a <check-stats> of their own.
	folds := 0
	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "sidebar toggle") {
			continue
		}
		folds++
		if !strings.Contains(line, "<check-stats>") {
			t.Errorf("this fold macro does not ask for the counts afterwards:\n%s", line)
		}
	}
	if folds != 2 {
		t.Errorf("got %d fold macros, want 2: the index, by two spellings of Enter", folds)
	}
}

// TestSidebarRCWalksTheHighlightBack counts the steps, which is the one number in this file that
// has to be exactly right: registering mailboxes scrolls the open one to the top of the sidebar, so
// the highlight is walked down from <sidebar-first> and a step too few lands on the wrong row.
func TestSidebarRCWalksTheHighlightBack(t *testing.T) {
	c, hosted, gmail := fixtures()
	c.SidebarCollapse = true
	accounts := []account.Account{hosted, gmail}

	// Nothing to go back to: no push at all, rather than a highlight moved for no reason.
	if got := SidebarRC(c, accounts, nil, ""); strings.Contains(got, "push ") {
		t.Error("pushed a highlight with no account to put it on")
	}

	// The FIRST account is row one, so <sidebar-first> alone.
	got := SidebarRC(c, accounts, nil, hosted.Address)
	if !strings.Contains(got, "push \"<sidebar-first>\"\n") {
		t.Errorf("row one should need no steps:\n%s", got)
	}

	// The second account, with the first FOLDED: row two.
	got = SidebarRC(c, accounts, nil, gmail.Address)
	if n := strings.Count(got, "<sidebar-next>"); n != 1 {
		t.Errorf("got %d steps to the second row, want 1", n)
	}

	// The second account with the first UNFOLDED: its five folders sit in between. Gmail's
	// folders include "[Gmail]/Sent Mail", whose space must not be counted as another mailbox.
	got = SidebarRC(c, accounts, map[string]bool{hosted.Address: true}, gmail.Address)
	want := strings.Count(mailboxLine(c, hosted), "\" \"") + 1 + 1
	if n := strings.Count(got, "<sidebar-next>"); n != want {
		t.Errorf("got %d steps past an unfolded account, want %d", n, want)
	}
	if want != 5 {
		t.Fatalf("the fixture changed: %d folders above, so this test is not measuring what it says", want-1)
	}
}
