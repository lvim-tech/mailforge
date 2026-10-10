package sidebar

import (
	"os"
	"testing"
)

// state redirects the state file into the test's own directory. XDG_STATE_HOME is the documented
// way, and using it here means the test exercises the same lookup the program does rather than a
// seam added for testing.
func state(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

func TestToggleRemembersAcrossRuns(t *testing.T) {
	state(t)

	var s State
	if open := s.Toggle("hello@example.com"); !open {
		t.Error("the first fold did not open the account")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	// A FRESH read, which is the thing being tested: NeoMutt is restarted, the program is run
	// again, and the folds have to be where they were left.
	again := Load()
	if !again.Open("hello@example.com") {
		t.Error("the account came back folded shut")
	}

	if open := again.Toggle("hello@example.com"); open {
		t.Error("the second fold did not shut the account")
	}
	if err := again.Save(); err != nil {
		t.Fatal(err)
	}
	if Load().Open("hello@example.com") {
		t.Error("shutting it was not remembered")
	}
}

// TestLoadSurvivesRubbish: the state is a convenience, and a NeoMutt that cannot start because a
// cache file has a byte wrong in it would be a much worse thing than a sidebar that comes up shut.
func TestLoadSurvivesRubbish(t *testing.T) {
	state(t)
	var s State
	s.Toggle("hello@example.com")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(pathForTest(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(); len(got.Expanded) != 0 || got.Current != "" {
		t.Errorf("unreadable state produced %+v, want nothing folded", got)
	}
}

// TestPruneForgetsRemovedAccounts: an address can outlive its account, and the day somebody adds
// one back at the same address it would come up folded open for reasons nobody could trace.
func TestPruneForgetsRemovedAccounts(t *testing.T) {
	s := State{Expanded: []string{"gone@example.com", "here@example.com"}, Current: "gone@example.com"}
	s.Prune(map[string]bool{"here@example.com": true})

	if s.Open("gone@example.com") {
		t.Error("an address with no account behind it stayed folded open")
	}
	if !s.Open("here@example.com") {
		t.Error("prune dropped an account that is still there")
	}
	if s.Current != "" {
		t.Errorf("current is %q, want empty: the account it named is gone", s.Current)
	}
}

// TestSetCurrentIsIdempotent. This runs from a folder-hook, which fires on every change of folder,
// so a write per call would be a write per keystroke in the sidebar for a value that rarely moves.
func TestSetCurrentIsIdempotent(t *testing.T) {
	state(t)
	if err := SetCurrent("hello@example.com"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(pathForTest())
	if err != nil {
		t.Fatal(err)
	}

	if err := SetCurrent("hello@example.com"); err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(pathForTest())
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(st.ModTime()) {
		t.Error("the same account was written again")
	}

	if err := SetCurrent("other@example.com"); err != nil {
		t.Fatal(err)
	}
	if Load().Current != "other@example.com" {
		t.Error("a changed account was not written")
	}
}

// TestCurrentSurvivesAFold is the pair the whole feature rests on: the folder-hook writes Current,
// the keypress writes Expanded, and each must leave the other alone.
func TestCurrentSurvivesAFold(t *testing.T) {
	state(t)
	if err := SetCurrent("hello@example.com"); err != nil {
		t.Fatal(err)
	}

	s := Load()
	s.Toggle(s.Current)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	got := Load()
	if got.Current != "hello@example.com" {
		t.Errorf("current is %q after a fold", got.Current)
	}
	if !got.Open("hello@example.com") {
		t.Error("the fold was lost")
	}
}

// TestTheTwoHooksAgreeAboutTheInbox is the ordering the whole fold key rests on.
//
// Two folder-hooks fire when an INBOX is opened and neither can see the other: the account's own
// assumes a plain folder, and the one defined after it corrects that. Opening a folder that is not
// an INBOX runs only the first, which has to leave the flag down.
func TestTheTwoHooksAgreeAboutTheInbox(t *testing.T) {
	state(t)

	// An INBOX: both hooks, in file order.
	if err := SetCurrent("hello@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := SetCurrentIsInbox(); err != nil {
		t.Fatal(err)
	}
	if got := Load(); !got.CurrentIsInbox || got.Current != "hello@example.com" {
		t.Errorf("after opening an INBOX: %+v", got)
	}

	// Sent, in the same account: the first hook alone, and the flag has to come down with it or
	// Enter would fold the account while the user was walking into one of its folders.
	if err := SetCurrent("hello@example.com"); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got.CurrentIsInbox {
		t.Error("the inbox flag stayed up after a plain folder was opened")
	}
}
