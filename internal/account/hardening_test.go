package account

import "testing"

// The address is not only a name: it becomes a path element, a folder-hook pattern and part of the
// `pass show` command line in two generated files. The renderer quotes, but a value that can only
// be rendered safely by remembering to quote it is a value the next call site will get wrong — so
// the odd characters are refused here, where the message can name the one that was wrong.
func TestValidateRejectsAddressesThatWouldReachTheShell(t *testing.T) {
	base := func(addr string) Account {
		return Account{
			Address: addr,
			IMAP:    Server{Host: "imap.example.com", Port: 993, Security: TLS},
			SMTP:    Server{Host: "smtp.example.com", Port: 465, Security: TLS},
		}
	}

	bad := []string{
		"a$(id)@example.com",
		"a`id`@example.com",
		"a;rm -rf ~@example.com",
		"a b@example.com",
		"a|b@example.com",
		"a'b@example.com",
		"a\"b@example.com",
		"a\nb@example.com",
	}
	for _, addr := range bad {
		if err := base(addr).Validate(); err == nil {
			t.Errorf("%q was accepted", addr)
		}
	}

	// And everything a real mailbox needs still passes, internationalised local parts included.
	good := []string{
		"someone@example.com",
		"first.last+tag@sub.example.co.uk",
		"bob_smith-1@example.com",
		"биser@example.com",
	}
	for _, addr := range good {
		if err := base(addr).Validate(); err != nil {
			t.Errorf("%q was refused: %v", addr, err)
		}
	}
}

// Copying a Registry by assignment shares the accounts array, so a background apply sorting or
// rewriting its copy writes into the one the interface is drawing from.
func TestCloneSharesNothing(t *testing.T) {
	r := Registry{Accounts: []Account{
		{Address: "a@example.com"},
		{Address: "b@example.com"},
	}}
	c := r.Clone()
	c.Accounts[0].Address = "changed@example.com"
	c.Accounts = append(c.Accounts, Account{Address: "c@example.com"})

	if r.Accounts[0].Address != "a@example.com" {
		t.Errorf("the original was modified through the clone: %q", r.Accounts[0].Address)
	}
	if len(r.Accounts) != 2 {
		t.Errorf("the original grew to %d accounts", len(r.Accounts))
	}
}

func TestFoldersAny(t *testing.T) {
	if (Folders{}).Any() {
		t.Error("an empty discovery reported names")
	}
	if (Folders{Delim: "."}).Any() {
		t.Error("a delimiter alone is not a folder name")
	}
	if !(Folders{Sent: "Sent"}).Any() {
		t.Error("a discovered name was not reported")
	}
}
