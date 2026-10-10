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

	got := SidebarRC(c, []account.Account{hosted, gmail}, nil, "", nil)

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

	got := SidebarRC(c, []account.Account{hosted, gmail}, map[string]bool{hosted.Address: true}, "", nil)

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

	got := SidebarRC(c, []account.Account{hosted}, map[string]bool{"deleted@example.com": true}, "", nil)

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
	if got := SidebarRC(c, accounts, nil, "", nil); strings.Contains(got, "push ") {
		t.Error("pushed a highlight with no account to put it on")
	}

	// The FIRST account is row one, so <sidebar-first> alone.
	got := SidebarRC(c, accounts, nil, hosted.Address, nil)
	if !strings.Contains(got, "push \"<sidebar-first>\"\n") {
		t.Errorf("row one should need no steps:\n%s", got)
	}

	// The second account, with the first FOLDED: row two.
	got = SidebarRC(c, accounts, nil, gmail.Address, nil)
	if n := strings.Count(got, "<sidebar-next>"); n != 1 {
		t.Errorf("got %d steps to the second row, want 1", n)
	}

	// The second account with the first UNFOLDED: its five folders sit in between. Gmail's
	// folders include "[Gmail]/Sent Mail", whose space must not be counted as another mailbox.
	got = SidebarRC(c, accounts, map[string]bool{hosted.Address: true}, gmail.Address, nil)
	want := strings.Count(mailboxLine(c, hosted), "\" \"") + 1 + 1
	if n := strings.Count(got, "<sidebar-next>"); n != want {
		t.Errorf("got %d steps past an unfolded account, want %d", n, want)
	}
	if want != 5 {
		t.Fatalf("the fixture changed: %d folders above, so this test is not measuring what it says", want-1)
	}
}

// TestTheFigureIsWhatWasAskedFor pins the format, because it is the whole point of writing the
// numbers into the label: "16[1/3]" is sixteen messages, one unread here, three unread elsewhere in
// the account — and the third of those is the one NeoMutt has no expando for.
func TestTheFigureIsWhatWasAskedFor(t *testing.T) {
	for _, tc := range []struct {
		n    SidebarCount
		want string
	}{
		{SidebarCount{Total: 16}, "16"},
		{SidebarCount{Total: 16, Unread: 1, Elsewhere: 3}, "16[1/3]"},
		// Unread only elsewhere still shows both halves: a bare "16" would say the account is
		// read when three messages in its folders are not.
		{SidebarCount{Total: 16, Elsewhere: 3}, "16[0/3]"},
		{SidebarCount{Total: 0, Unread: 0}, "0"},
	} {
		if got := accountFigure(tc.n); got != tc.want {
			t.Errorf("accountFigure(%+v) = %q, want %q", tc.n, got, tc.want)
		}
	}
	// A folder has no elsewhere of its own, and an Elsewhere set on one must not leak into its
	// figure.
	if got := folderFigure(SidebarCount{Total: 13, Unread: 2, Elsewhere: 9}); got != "13[2]" {
		t.Errorf("folderFigure = %q, want 13[2]", got)
	}
	if got := folderFigure(SidebarCount{Total: 13}); got != "13" {
		t.Errorf("folderFigure = %q, want 13", got)
	}
}

// TestTheFigureSitsAtTheRightEdge: with sidebar_format down to %D, the padding in the label is the
// only thing aligning the numbers, and one column has to be left for NeoMutt's divider.
func TestTheFigureSitsAtTheRightEdge(t *testing.T) {
	got := sidebarLabel(40, "MOTION-AGENCY.COM | HELLO", "16[1/3]")
	if len(got) != 39 {
		t.Errorf("label is %d columns, want 39 — one short of the width", len(got))
	}
	if !strings.HasSuffix(got, "16[1/3]") {
		t.Errorf("the figure is not at the end: %q", got)
	}

	// A label with no room left keeps its figure and one space rather than running into it.
	long := sidebarLabel(12, "A-VERY-LONG-ACCOUNT-LABEL", "9")
	if !strings.HasSuffix(long, " 9") {
		t.Errorf("a too-long label lost its figure: %q", long)
	}
}

// TestCountsRCRelabelsWithoutReRegistering is the file the timeout-hook reads, and the two things
// that must NOT be in it are what make a live refresh possible at all: `unmailboxes *` would scroll
// the open mailbox to the top every few seconds, and a push would drag the highlight with it.
func TestCountsRCRelabelsWithoutReRegistering(t *testing.T) {
	c, hosted, gmail := fixtures()
	c.SidebarCollapse = true
	accounts := []account.Account{hosted, gmail}
	expanded := map[string]bool{hosted.Address: true}
	got := CountsRC(c, accounts, expanded, nil)
	// Per LINE, the way NeoMutt reads it: the file explains in a comment that it deliberately
	// has no unmailboxes, and a substring search finds that sentence and calls it a command.
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "unmailboxes") {
			t.Error("the refresh clears the mailbox list, which scrolls the sidebar to the open row")
		}
		if strings.HasPrefix(line, "push ") {
			t.Error("the refresh pushes the highlight, which would move it under the user's hands")
		}
	}

	// The SAME rows as the real list, or the refresh adds what it names: folders of a folded
	// account appearing at the bottom, which is what happened the first time this was tried.
	full := SidebarRC(c, accounts, expanded, "", nil)
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "named-mailboxes ") {
			continue
		}
		if !strings.Contains(full, line) {
			t.Errorf("the refresh names a row the list does not have:\n%s", line)
		}
	}
	if a, b := strings.Count(got, "named-mailboxes "), strings.Count(full, "named-mailboxes "); a != b {
		t.Errorf("refresh has %d rows, the list has %d", a, b)
	}
}

// TestRefreshRCCountsThenShows covers the ordering the hook depends on: a backtick in a hook's own
// command runs when the hook is PARSED, so the counting has to live in a sourced file, where it runs
// each time the file is read — and the result has to be read after it is written.
func TestRefreshRCCountsThenShows(t *testing.T) {
	c, _, _ := fixtures()
	got := RefreshRC(c)

	count := strings.Index(got, "`mailforge sidebar counts`")
	show := strings.Index(got, "source "+CountsPath(c))
	if count < 0 || show < 0 {
		t.Fatalf("the refresh is missing one of its two halves:\n%s", got)
	}
	if count > show {
		t.Error("the labels are read before they are written, so every refresh shows the previous one")
	}
}
