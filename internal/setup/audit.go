package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/edit"
	"github.com/lvim-tech/mailforge/internal/keys"
	"github.com/lvim-tech/mailforge/internal/render"
	"github.com/lvim-tech/mailforge/internal/sidebar"
	"github.com/lvim-tech/mailforge/internal/store"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// Audit checks that what is on disk still agrees with the registry.
//
// This is the product. Every other feature exists so that this check can be green: the drift it
// looks for — a rendered file edited by hand, a pass entry deleted, an mbsync block that no
// account claims — is exactly what a hand-maintained setup accumulates silently over years.
//
// It compares against a FRESH RENDER rather than against a checksum stored somewhere. A stored
// checksum only proves the file has not changed; re-rendering proves it says what it should say
// today, including after the templates themselves change.
func Audit(c config.Config, reg account.Registry) Report {
	var r Report

	// The keys, first and unconditionally: they are a function of the config alone, so they are
	// worth checking on a machine that has no accounts yet — and a broken keys.rc is an error
	// banner at every start plus a key that does nothing, which looks like anything but a bad file.
	auditKeys(&r, c)

	auditOptions(&r, c)
	auditTimer(&r, c)
	auditSidebar(&r, c, reg)

	// The seeded user files, also unconditionally: they are the ones neomuttrc sources, and
	// FirstRun cannot put back one that was never written on this machine.
	auditSeeds(&r, c)

	if len(reg.Accounts) == 0 {
		r.Findings = append(r.Findings, Finding{
			Warn, "registry", "no accounts yet", "the Accounts tab, then [a]",
		})
		return r
	}

	// The index and the include line: derived from every account at once, so they drift as soon
	// as one is added or removed outside the program.
	idx := filepath.Join(c.NeomuttDir, "mailforge", "accounts.rc")
	checkOwned(&r, "accounts.rc", idx, render.AccountsRC(c, reg.Accounts))

	rc := filepath.Join(c.NeomuttDir, "neomuttrc")
	switch has, err := edit.Has(rc, ""); {
	case err != nil:
		r.Findings = append(r.Findings, Finding{Fail, "neomuttrc", err.Error(),
			"fix the markers by hand, then apply again"})
	case !has:
		r.Findings = append(r.Findings, Finding{Fail, "neomuttrc",
			rc + " does not source the mailforge index — NeoMutt knows about none of this",
			"apply any account, or run first-run setup"})
	default:
		r.Findings = append(r.Findings, Finding{OK, "neomuttrc", "sources the mailforge index", ""})
	}

	for _, a := range reg.Accounts {
		auditAccount(&r, c, a)
	}

	// Blocks in mbsyncrc that no account claims: what an account left behind when it was
	// removed by hand, or when its address changed. Silent, and it keeps fetching.
	if orphans, err := orphanBlocks(c, reg); err != nil {
		r.Findings = append(r.Findings, Finding{Fail, "mbsyncrc", err.Error(),
			"fix the markers by hand, then apply again"})
	} else {
		for _, id := range orphans {
			r.Findings = append(r.Findings, Finding{
				Warn, "mbsyncrc",
				"a block for " + id + " that no account claims — it will still be fetched",
				"add that account back, or remove the block by hand",
			})
		}
	}

	return r
}

// auditKeys checks the generated keys file, the line that sources it, and whether the user's own
// bindings quietly override it.
func auditKeys(r *Report, c config.Config) {
	kf := filepath.Join(c.NeomuttDir, "mailforge", "keys.rc")
	checkOwned(r, "keys", kf, render.KeysRC(c))

	rc := filepath.Join(c.NeomuttDir, "neomuttrc")
	switch has, err := edit.Has(rc, "keys"); {
	case err != nil:
		r.Findings = append(r.Findings, Finding{Fail, "keys", err.Error(),
			"fix the markers by hand, then apply again"})
	case !has:
		r.Findings = append(r.Findings, Finding{Warn, "keys",
			rc + " does not source keys.rc — the bindings in the Keys tab do nothing",
			"the Doctor's [f], or apply any account"})
	default:
		r.Findings = append(r.Findings, Finding{OK, "keys", "sourced from neomuttrc", ""})
	}

	// Bindings in the user's own file that land on a managed key.
	//
	// Not a fault — bindings.rc is sourced afterwards precisely so it can win — but it is the
	// one way the Keys tab can show one thing while NeoMutt does another, and somebody would
	// spend an evening on that.
	for _, name := range shadowed(c) {
		r.Findings = append(r.Findings, Finding{Warn, "keys",
			"your bindings.rc rebinds the key for " + name + ", and is read after keys.rc — " +
				"what you set here has no effect on it",
			"remove that line from bindings.rc, or change the action here to a free key"})
	}
}

// auditOptions checks the generated settings file and the line that sources it.
func auditOptions(r *Report, c config.Config) {
	of := filepath.Join(c.NeomuttDir, "mailforge", "options.rc")
	checkOwned(r, "options", of, render.OptionsRC(c))

	mf := filepath.Join(c.NeomuttDir, "mailforge", "mailcap")
	checkOwned(r, "options", mf, render.MailcapRC(c))
	auditHTMLViewer(r, c)

	rc := filepath.Join(c.NeomuttDir, "neomuttrc")
	switch has, err := edit.Has(rc, "options"); {
	case err != nil:
		r.Findings = append(r.Findings, Finding{Fail, "options", err.Error(),
			"fix the markers by hand, then apply again"})
	case !has:
		r.Findings = append(r.Findings, Finding{Warn, "options",
			rc + " does not source options.rc — what Settings sets here does not reach NeoMutt",
			"the Doctor's [f], or apply any account"})
	default:
		r.Findings = append(r.Findings, Finding{OK, "options", "sourced from neomuttrc", ""})
	}

	// A setting of the user's own that lands on one this program manages.
	//
	// Not a fault — settings.rc is sourced afterwards precisely so it can win — but it is the
	// one way the Settings tab can show one thing while NeoMutt does another. The editor is
	// the case that cost an evening: the seeded `set editor = "$EDITOR"` sat in that file,
	// expanded to nothing on a start that did not export it, and composing ended in an error
	// naming a variable rather than a missing editor.
	for _, name := range []string{"editor", "timeout", "mail_check", "mail_check_stats",
		"mail_check_stats_interval", "wait_key"} {
		if setsIn(c, name) {
			r.Findings = append(r.Findings, Finding{Warn, "options",
				"your settings.rc sets " + name + ", and is read after options.rc — what " +
					"Settings says here has no effect on it",
				"remove that line from settings.rc, or leave " + name + " to mailforge"})
		}
	}
}

// setsIn reports whether the user's own settings.rc sets a variable.
func setsIn(c config.Config, name string) bool {
	b, err := os.ReadFile(filepath.Join(c.NeomuttDir, "settings.rc"))
	if err != nil {
		return false // no file of their own is not a finding
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) >= 2 && (f[0] == "set" || f[0] == "unset") && strings.TrimSuffix(f[1], "=") == name {
			return true
		}
	}
	return false
}

// auditTimer checks the fetch timer against what the config asks for.
//
// Both halves, because they fail differently: a unit file that no longer matches is a schedule
// waiting for the next apply, while a matching file that systemd never enabled is a schedule that
// simply does not run — and the second is invisible from the settings screen.
func auditTimer(r *Report, c config.Config) {
	tmr := filepath.Join(render.SystemdUserDir(), render.TimerUnit)

	if c.SyncEvery <= 0 {
		if _, err := os.Stat(tmr); err == nil {
			r.Findings = append(r.Findings, Finding{Warn, "timer",
				"the fetch timer is off in settings but its unit is still on disk",
				"the Doctor's [f] removes it"})
			return
		}
		r.Findings = append(r.Findings, Finding{OK, "timer", "off — nothing fetches on its own", ""})
		return
	}

	checkOwned(r, "timer", tmr, render.MbsyncTimer(c))
	checkOwned(r, "timer", filepath.Join(render.SystemdUserDir(), render.ServiceUnit),
		render.MbsyncService(c))

	if !sys.Have("systemctl") {
		return
	}
	res, err := systemctl("is-active", render.TimerUnit)
	if err != nil || strings.TrimSpace(res.Stdout) != "active" {
		r.Findings = append(r.Findings, Finding{Warn, "timer",
			render.TimerUnit + " is not running — the files are there, systemd is not using them",
			"the Doctor's [f], or: systemctl --user enable --now " + render.TimerUnit})
		return
	}
	r.Findings = append(r.Findings, Finding{OK, "timer",
		"active, every " + render.HumanEvery(c.SyncEvery), ""})

	// How long that timer lives. A user timer dies with the login session unless systemd is
	// told to keep the user's services running, and "the timer is active" is true right up to
	// the moment you log out — which is exactly when nobody is watching to notice.
	if !sys.Have("loginctl") {
		return
	}
	switch lingering := Lingering(); {
	case lingering == c.FetchWhenLoggedOut && lingering:
		r.Findings = append(r.Findings, Finding{OK, "timer", "keeps running after logout", ""})
	case lingering == c.FetchWhenLoggedOut:
		r.Findings = append(r.Findings, Finding{OK, "timer",
			"only while you are logged in — as configured", ""})
	case c.FetchWhenLoggedOut:
		r.Findings = append(r.Findings, Finding{Warn, "timer",
			"set to keep fetching after logout, but this user does not linger — the timer stops " +
				"when the session ends",
			"the Doctor's [f], or: loginctl enable-linger $USER"})
	default:
		r.Findings = append(r.Findings, Finding{Warn, "timer",
			"this user lingers, so the timer keeps fetching after logout — settings say it should not",
			"the Doctor's [f], or: loginctl disable-linger $USER"})
	}
}

// auditSeeds checks that the files seeded for the user are still on disk.
//
// Their CONTENTS are never checked: they belong to the user from the moment they exist, and a
// program comparing somebody's colour scheme against a template would be reporting the user's own
// work as drift. Only absence is a finding, because absence is what NeoMutt trips over.
func auditSeeds(r *Report, c config.Config) {
	for _, s := range Seeds(c) {
		p := filepath.Join(c.NeomuttDir, s.Name)
		if _, err := os.Stat(p); err != nil {
			r.Findings = append(r.Findings, Finding{Warn, "seed",
				p + " is missing — " + s.Why, "the Doctor's [f] writes it back"})
			continue
		}
		r.Findings = append(r.Findings, Finding{OK, "seed", s.Name + " is there", ""})
	}
}

// shadowed lists managed actions whose key the user's own bindings.rc binds in an overlapping menu.
func shadowed(c config.Config) []string {
	b, err := os.ReadFile(filepath.Join(c.NeomuttDir, "bindings.rc"))
	if err != nil {
		return nil // no file of their own is not a finding
	}
	bound := keys.Fill(c.Keys)

	var out []string
	for _, a := range keys.Actions() {
		k := bound[a.Name()]
		if k == "" {
			continue
		}
		if bindsIn(string(b), a.Menu, k) {
			out = append(out, a.Name())
		}
	}
	sort.Strings(out)
	return out
}

// bindsIn reports whether the file binds key in this menu.
func bindsIn(file string, menu string, key string) bool {
	for _, l := range strings.Split(file, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		// "bind <menus> <key> <function>" — anything shorter is not a binding.
		if len(f) < 4 || (f[0] != "bind" && f[0] != "macro") || f[2] != key {
			continue
		}
		// The user's own file may bind several menus at once — "bind index,pager J …".
		for _, m := range strings.Split(f[1], ",") {
			if m == menu {
				return true
			}
		}
	}
	return false
}

func auditAccount(r *Report, c config.Config, a account.Account) {
	// The rc file, owned whole: a mismatch means somebody edited a generated file, and the next
	// apply will silently throw their work away. Better to say so now.
	checkOwned(r, a.Address, a.RCFile(c), render.AccountRC(c, a))

	// The mbsync block, between markers.
	body, ok, err := edit.Body(c.MbsyncRC, a.Address)
	switch {
	case err != nil:
		r.Findings = append(r.Findings, Finding{Fail, a.Address, err.Error(),
			"fix the markers by hand, then apply again"})
	case !ok:
		r.Findings = append(r.Findings, Finding{Fail, a.Address,
			"no mbsync block — this account is configured for reading but nothing fetches it",
			"apply the account again"})
	case body != render.MbsyncBlock(c, a):
		r.Findings = append(r.Findings, Finding{Warn, a.Address,
			"the mbsync block differs from what would be generated now",
			"apply the account again to bring it back"})
	default:
		r.Findings = append(r.Findings, Finding{OK, a.Address, "mbsync block matches", ""})
	}

	// The password. Its absence is the failure that shows up as a prompt loop at fetch time
	// with no explanation, so it is worth its own line.
	cands := a.PassCandidates(c)
	switch found := store.Existing(cands); {
	case len(found) == 0:
		r.Findings = append(r.Findings, Finding{Fail, a.Address,
			"no password at " + strings.Join(cands, " or ") +
				" — mbsync and NeoMutt will both fail to log in",
			"edit the account and enter the password again"})

	case len(found) > 1:
		// Two encrypted files, one password, nothing on disk saying which is current. It is a
		// warning and not a failure because logging in still works — one of them is read, and
		// it may well be the right one. What it costs is the next rotation: changing the
		// password updates one file and leaves the other as a plausible, stale answer.
		r.Findings = append(r.Findings, Finding{Warn, a.Address,
			"the password is stored twice, at " + strings.Join(found, " and "),
			"open the account and choose which entry to use — the other can then be removed"})

	case found[0] != a.PassEntryIn(c, c.PassLayout):
		// Found, but not where the configured layout says to look. Worth its own line: it is
		// the difference between "mailforge agrees with your store" and "mailforge is reading
		// something it had to go looking for", and only the second gets silently undone by a
		// change to pass_layout.
		r.Findings = append(r.Findings, Finding{OK, a.Address,
			"password present at " + found[0] + ", found outside the " +
				string(c.PassLayout) + " layout", ""})

	default:
		r.Findings = append(r.Findings, Finding{OK, a.Address, "password present at " + found[0], ""})
	}

	// The signature file, when there is one to have.
	//
	// Only its presence and contents are checked against the registry — the same rule as every
	// other owned file. An account with no signature has no file, and that is not a finding.
	if strings.TrimSpace(a.Signature) != "" {
		checkOwned(r, a.Address, a.SigFile(c), signatureBody(a))
	}

	// A folder name that is still the SERVER's spelling.
	//
	// mbsync stores the mailbox a server calls "INBOX.Sent" in a directory called "Sent", and
	// NeoMutt reads the directory. The translation needs the server's delimiter, learned during
	// verification; without it the rendered rc points at a path that exists on the server and
	// nowhere on this disk, and mbsync refuses the same name locally as ambiguous. Nothing
	// errors — sent mail is simply filed into a directory nothing syncs.
	if a.Folders.Delim == "" {
		for _, f := range []string{a.Folders.Sent, a.Folders.Drafts, a.Folders.Trash,
			a.Folders.Archive, a.Folders.Spam} {
			if f == "" || !strings.ContainsAny(f, "./") {
				continue
			}
			r.Findings = append(r.Findings, Finding{Warn, a.Address,
				f + " looks like a server path, but no hierarchy delimiter was recorded — " +
					"NeoMutt is being pointed at a mailbox that does not exist locally",
				"edit the account and let it verify again, which learns the delimiter"})
			break
		}
	}

	// The maildir.
	if st, err := os.Stat(a.Maildir(c)); err != nil || !st.IsDir() {
		r.Findings = append(r.Findings, Finding{Warn, a.Address,
			a.Maildir(c) + " does not exist — nothing has been fetched yet",
			"run: mbsync " + a.Address})
	}

	// How long since anything proved this account still works. Not an error — servers do not
	// stop working on a schedule — but a year-old answer to "does this log in" is not an answer.
	switch {
	case a.Verified.IsZero():
		r.Findings = append(r.Findings, Finding{Warn, a.Address,
			"never verified against the servers", "the Accounts tab, then [v]"})
	case time.Since(a.Verified) > 180*24*time.Hour:
		r.Findings = append(r.Findings, Finding{Warn, a.Address,
			fmt.Sprintf("last verified %d days ago", int(time.Since(a.Verified).Hours()/24)),
			"the Accounts tab, then [v]"})
	}
}

// checkOwned compares a file mailforge owns against what it would write today.
func checkOwned(r *Report, subject, path, want string) {
	got, err := os.ReadFile(path)
	switch {
	case err != nil:
		r.Findings = append(r.Findings, Finding{Fail, subject,
			path + " is missing", "apply the account again"})
	case string(got) != want:
		r.Findings = append(r.Findings, Finding{Warn, subject,
			path + " differs from what would be generated now — a hand edit here is lost on the " +
				"next apply", "apply the account again, and put the change in the registry instead"})
	default:
		r.Findings = append(r.Findings, Finding{OK, subject, filepath.Base(path) + " matches", ""})
	}
}

// orphanBlocks finds mbsync blocks with no account behind them.
func orphanBlocks(c config.Config, reg account.Registry) ([]string, error) {
	ids, err := edit.IDs(c.MbsyncRC)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := reg.Find(id); !ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// Fix re-applies everything that is a pure re-render, which is every drift class the audit can
// repair without asking: an edited owned file, a stale mbsync block, a missing index.
//
// It deliberately cannot fix a missing password or a missing maildir — those need a person, and a
// button that claims to fix everything would have to lie about two of them.
func Fix(c config.Config, reg *account.Registry) Plan {
	var p Plan
	run := func(what string, fn func() (string, error)) {
		s := Step{What: what}
		note, err := fn()
		s.Done, s.Note, s.Err = err == nil, note, err
		p.Steps = append(p.Steps, s)
	}

	// A seed this machine never received. CREATED, never overwritten — the check above is
	// existence only, so a file that is there is left exactly as the user left it.
	for _, s := range Seeds(c) {
		seed := s
		path := filepath.Join(c.NeomuttDir, seed.Name)
		run("write "+path, func() (string, error) {
			if _, err := os.Stat(path); err == nil {
				return "already there, left alone", nil
			}
			return "written", config.WriteAtomic(path, []byte(seed.Body), 0o600)
		})
	}

	for _, a := range reg.Accounts {
		acct := a
		run("re-render "+acct.RCFile(c), func() (string, error) {
			if err := os.MkdirAll(filepath.Dir(acct.RCFile(c)), 0o700); err != nil {
				return "", err
			}
			body := render.AccountRC(c, acct)
			if same, err := unchanged(acct.RCFile(c), body); err == nil && same {
				return "already up to date", nil
			}
			return "written", config.WriteAtomic(acct.RCFile(c), []byte(body), 0o600)
		})
		run("re-render the mbsync block for "+acct.Address, func() (string, error) {
			res, err := edit.Upsert(c.MbsyncRC, acct.Address, render.MbsyncBlock(c, acct), false)
			return res.Note, err
		})
	}
	ApplyIndex(c, reg, &p)
	ApplyConfig(c, &p)
	return p
}

/*
auditHTMLViewer reports the three ways HTML mail ends up unreadable without saying so.

All three are the same fault: something is in place, nothing complains, and every HTML message
opens as an empty body. It went unnoticed here for months.
*/
func auditHTMLViewer(r *Report, c config.Config) {
	program, _ := c.HTMLViewerCommand()
	choice := strings.TrimSpace(c.HTMLViewer)
	if choice == "" {
		choice = config.HTMLViewerAuto
	}

	// Asked for by name and not installed. Not silently replaced with the other one: naming a
	// program is a decision, and a program that quietly runs something else is worse than one
	// that says it cannot.
	if program == "" && choice != config.HTMLViewerNone {
		if choice == config.HTMLViewerAuto {
			r.Findings = append(r.Findings, Finding{Warn, "options",
				"no HTML viewer is installed, so HTML mail shows as an empty message",
				"install w3m (or lynx) and apply again, or set the HTML viewer to none " +
					"so it is left as an attachment"})
		} else {
			r.Findings = append(r.Findings, Finding{Warn, "options",
				choice + " is set as the HTML viewer but is not installed, so HTML mail " +
					"shows as an empty message",
				"install " + choice + ", or set the HTML viewer to auto"})
		}
	}

	// The user's own mailcap is read FIRST, so a text/html rule there beats the managed one —
	// which is the point of the ordering, and also the way a rule naming a program nobody has
	// goes on winning forever. mailforge seeded exactly such a rule before 2026-09-05.
	userMailcap := filepath.Join(c.NeomuttDir, "mailcap")
	if rule, ok := htmlRuleIn(userMailcap); ok {
		if prog := firstWord(rule); prog != "" {
			if _, err := exec.LookPath(prog); err != nil {
				r.Findings = append(r.Findings, Finding{Warn, "options",
					"your mailcap has a text/html rule running " + prog + ", which is not " +
						"installed — it is read before mailforge's and wins, so HTML mail " +
						"shows as an empty message",
					"remove that line from " + userMailcap + " and let the HTML viewer " +
						"setting handle it"})
			}
		}
	}

	// A mailcap_path in the user's settings.rc is read after options.rc and undoes the pair of
	// files, leaving the managed mailcap unreachable.
	if hasSetting(filepath.Join(c.NeomuttDir, "settings.rc"), "mailcap_path") {
		r.Findings = append(r.Findings, Finding{Warn, "options",
			"your settings.rc sets mailcap_path, and is read after options.rc — mailforge's " +
				"own mailcap is never reached",
			"remove that line; mailforge points NeoMutt at your mailcap first and its own second"})
	}
}

// htmlRuleIn returns the first uncommented text/html line of a mailcap file.
func htmlRuleIn(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "text/html;") {
			return strings.TrimSpace(line[len("text/html;"):]), true
		}
	}
	return "", false
}

// hasSetting says whether a NeoMutt rc file sets a named variable, ignoring comments.
func hasSetting(path, name string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "set ") && strings.Contains(line, name) {
			return true
		}
	}
	return false
}

// firstWord is the program a mailcap command runs.
func firstWord(cmd string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(cmd), " ", 2)[0])
}

/*
auditSidebar checks the folded sidebar: the one generated file that is ALLOWED to differ from what
an apply would write.

That is why it gets a check of its own instead of checkOwned. sidebar.rc is rewritten by a keypress
— that is the whole feature — so "differs from what would be generated now" is its normal state and
reporting it would train the user to ignore the doctor.

What can actually go wrong is different in kind: the file is missing, so the sidebar comes up empty;
or `mailforge` is not where NeoMutt's shell can find it, so the key presses and nothing happens; or
the setting was turned off and left a file behind that nothing sources.
*/
func auditSidebar(r *Report, c config.Config, reg account.Registry) {
	sb := render.SidebarPath(c)

	if !c.SidebarCollapse {
		for _, gone := range []string{sb, render.InboxHookPath(c)} {
			if _, err := os.Stat(gone); err == nil {
				r.Findings = append(r.Findings, Finding{Warn, "sidebar",
					gone + " is still there while folding is off, and nothing sources it",
					"apply any account and it is removed"})
			}
		}
		return
	}

	// The INBOX hook's file IS checked against what would be generated, unlike sidebar.rc below:
	// it has one line and no state in it, so any difference is a hand edit that the next apply
	// takes away. Missing, it is an error at every start from the hook that sources it.
	checkOwned(r, "sidebar", render.InboxHookPath(c), render.InboxHookRC(c))

	// Missing is a FAIL, not a warning: accounts.rc sources this file and registers no mailbox
	// of its own, so without it NeoMutt starts with an empty sidebar and no error anywhere.
	if _, err := os.Stat(sb); err != nil {
		r.Findings = append(r.Findings, Finding{Fail, "sidebar",
			sb + " is missing, and folding is on — the sidebar will be empty",
			"apply any account, or run `mailforge sidebar render`"})
	} else {
		st := sidebar.Load()
		r.Findings = append(r.Findings, Finding{OK, "sidebar",
			fmt.Sprintf("folded — %d of %d accounts open", len(st.OpenSet()), len(reg.Accounts)), ""})
	}

	// Both halves of this feature run the program by name: the fold key through <shell-escape>
	// and the folder-hook through a backtick. Found by this process is not proof it is found by
	// NeoMutt's shell, but NOT being found here is proof enough that something is wrong.
	if _, err := exec.LookPath("mailforge"); err != nil {
		r.Findings = append(r.Findings, Finding{Warn, "sidebar",
			"`mailforge` is not on PATH — Enter cannot fold anything and the folder-hook " +
				"cannot say which account is open",
			"put the binary somewhere on PATH, the same place `mailforge fetch` is found from"})
	}

	// An action in the Keys tab put on Enter. The fold macro is written at the END of keys.rc,
	// so it is the one NeoMutt keeps — the row would show a key that does something else, which
	// is the exact failure keys.Conflicts exists to prevent and cannot see here: the fold is a
	// macro of its own, not a row in the table.
	bound := keys.Fill(c.Keys)
	for _, a := range keys.Actions() {
		if a.Menu != "index" && a.Menu != "pager" {
			continue
		}
		k := keys.Canonical(bound[a.Name()])
		if k != "\n" && k != "\r" {
			continue
		}
		r.Findings = append(r.Findings, Finding{Warn, "sidebar",
			a.Name() + " is on Enter, and the fold key is written after it in keys.rc — " +
				"folding wins and that row does nothing",
			"give " + a.Name() + " another key, or turn folding off"})
	}

	// Enter is the fold key, and bindings.rc is read after keys.rc. Same shape as the keys
	// audit, and worth its own line because the symptom is a key that opens mail instead of
	// folding, which looks like the feature was never turned on.
	if b, err := os.ReadFile(filepath.Join(c.NeomuttDir, "bindings.rc")); err == nil {
		for _, k := range []string{`\n`, `\r`} {
			if bindsIn(string(b), "index", k) {
				r.Findings = append(r.Findings, Finding{Warn, "sidebar",
					"your bindings.rc binds Enter in the index, and is read after keys.rc — " +
						"it wins over the fold key",
					"remove that line from bindings.rc, or keep it and fold with it instead"})
				break
			}
		}
	}
}
