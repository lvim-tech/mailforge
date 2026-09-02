package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/lvim-tech/mailforge/internal/setup"
	"github.com/lvim-tech/mailforge/internal/sys"
	"github.com/lvim-tech/mailforge/internal/verify"
)

func (v *accountsView) Render(w, h int) string {
	switch v.mode {
	case mIdentity:
		return v.renderForm(w, "Step 1 of 4 — who", []labelled{
			{"address", &v.fAddress}, {"real name", &v.fReal}, {"label", &v.fLabel},
		}, "The label is the sidebar header. Left empty it is derived from the address.")
	case mServers:
		return v.renderServers(w)
	case mPassword:
		return v.renderPassword(w)
	case mVerify:
		return v.renderVerify(w)
	case mFolders:
		return v.renderFolders(w)
	case mApply:
		return v.renderApply(w, h)
	case mConfirmDelete:
		return v.renderDelete(w)
	}
	return v.renderList(w, h)
}

type labelled struct {
	label string
	in    *input
}

func (v *accountsView) renderForm(w int, title string, fields []labelled, note string) string {
	var b strings.Builder
	b.WriteString("  " + stAccent.Render(title) + "\n\n")
	all := v.fields()
	for i, f := range fields {
		focused := i < len(all) && all[i] == f.in && v.field == i
		// The input draws its own label; a second one beside it was the same word twice.
		b.WriteString("  " + f.in.render(focused) + "\n")
	}
	if note != "" {
		b.WriteString("\n" + indent(stDim.Render(wrapText(note, maxi(w-6, 30))), 2) + "\n")
	}
	if v.busy != "" {
		b.WriteString("\n  " + stWarn.Render(v.busy+"…") + "\n")
	}
	return b.String()
}

func (v *accountsView) renderServers(w int) string {
	var b strings.Builder
	b.WriteString("  " + stAccent.Render("Step 2 of 4 — where") + "\n\n")

	fields := []labelled{
		{"IMAP host", &v.fIHost}, {"IMAP port", &v.fIPort},
		{"IMAP security", &v.fISec}, {"IMAP login", &v.fIUser},
		{"SMTP host", &v.fSHost}, {"SMTP port", &v.fSPort},
		{"SMTP security", &v.fSSec}, {"SMTP login", &v.fSUser},
	}
	for i, f := range fields {
		if i == 4 {
			b.WriteString("\n")
		}
		b.WriteString("  " + f.in.render(v.field == i) + "\n")
	}

	if v.busy != "" {
		b.WriteString("\n  " + stWarn.Render(v.busy+"…") + "\n")
	}
	if v.source != "" {
		// Where a guess CAME FROM decides how much of it to trust, so it is named rather than
		// presented as fact: "the domain's own SRV records" and "probed" deserve different
		// amounts of squinting from the person about to accept them.
		b.WriteString("\n  " + stDim.Render("source: ") + stFg.Render(v.source) + "\n")
	}
	if v.note != "" {
		b.WriteString("\n" + indent(stNote.Render(wrapText(v.note, maxi(w-6, 30))), 2) + "\n")
	}
	return b.String()
}

func (v *accountsView) renderPassword(w int) string {
	var b strings.Builder
	b.WriteString("  " + stAccent.Render("Step 3 of 4 — the password") + "\n\n")

	// TWO fields, and the path first. Where a password is written is not a detail beneath the
	// password: it decides which of several encrypted files mbsync will read at four in the
	// morning, and a store shared with keyforge has more than one plausible answer. Deriving it
	// silently is what let mailforge write a second copy of a secret it did not own.
	b.WriteString("  " + v.fPassPath.render(v.field == 0) + "\n\n")
	b.WriteString("  " + v.pass.render(v.field == 1) + "\n\n")

	// The one line that changes what to DO goes FIRST, on its own, and lit.
	//
	// It used to be appended to the paragraph below — same colour, same block — and a fact that
	// decides whether to type anything at all read as more boilerplate. Somebody looking at this
	// screen for a password they had already stored could not find the sentence saying so.
	switch {
	case len(v.passDup) > 1:
		// Two files, one password. This outranks every other line on the screen: whichever is
		// left behind stays readable and plausible forever, and the next rotation updates only
		// one of them.
		b.WriteString(indent(stBad.Render(wrapText(
			"! the password is stored twice, at "+strings.Join(v.passDup, " and ")+
				" — put the one to keep in the field above; the other is left untouched",
			maxi(w-6, 30))), 2) + "\n\n")

	case v.stored:
		what := "a password is already stored as " + v.passEntryChosen()
		how := "press enter to use it, type a new one, or [ctrl+o] to change it in " + sys.KeyforgeBin
		b.WriteString(indent(stOK.Render(wrapText("✓ "+what+" — "+how, maxi(w-6, 30))), 2) + "\n\n")

	default:
		b.WriteString(indent(stDim.Render(wrapText(
			"nothing is stored at "+v.passEntryChosen()+" yet — type a password, or [ctrl+o] to "+
				"have "+sys.KeyforgeBin+" generate one under the rules you set there",
			maxi(w-6, 30))), 2) + "\n\n")
	}

	msg := "Typed into locked memory, checked against the servers, and only then written to pass " +
		"as " + v.passEntryChosen() + ". It never becomes an ordinary string in this program, and " +
		"neither does one that comes back from " + sys.KeyforgeBin + "."
	if v.stored {
		msg += " Replacing it rewrites only the password line; a login, a URL or a note kept " +
			"under it stays."
	}
	b.WriteString(indent(stDim.Render(wrapText(msg, maxi(w-6, 30))), 2) + "\n")
	if v.note != "" {
		b.WriteString("\n" + indent(stNote.Render(wrapText(v.note, maxi(w-6, 30))), 2) + "\n")
	}
	return b.String()
}

func (v *accountsView) renderVerify(w int) string {
	var b strings.Builder
	b.WriteString("  " + stAccent.Render("Step 4 of 4 — proving it works") + "\n\n")
	if v.busy != "" {
		b.WriteString("  " + stWarn.Render(v.busy+"…") + "\n\n")
		b.WriteString(indent(stDim.Render(wrapText(
			"Nothing has been written yet. If either check fails, no file on this machine will "+
				"have been touched.", maxi(w-6, 30))), 2))
		return b.String()
	}
	b.WriteString(v.resultLine(w, "IMAP", v.draft.IMAP.Host, v.draft.IMAP.Port, v.imapRes))
	b.WriteString(v.resultLine(w, "SMTP", v.draft.SMTP.Host, v.draft.SMTP.Port, v.smtpRes))

	b.WriteString("\n")
	switch {
	case v.imapRes.OK && v.smtpRes.OK:
		b.WriteString(indent(stGood.Render("both sides answered — press enter to go on"), 2))
	case v.imapRes.Fatal() || v.smtpRes.Fatal():
		b.WriteString(indent(stDim.Render(wrapText(
			"This failed before the password was ever offered, so it is the server details that "+
				"are wrong, not what you typed. [esc] twice goes back to them.",
			maxi(w-6, 30))), 2))
	default:
		b.WriteString(indent(stDim.Render(wrapText(
			"The servers answered and refused the credentials. [esc] goes back to the password.",
			maxi(w-6, 30))), 2))
	}
	return b.String()
}

// resultLine reports one protocol: how far it got, and the server's own words when it failed.
func (v *accountsView) resultLine(w int, what, host string, port int, r verify.Result) string {
	head := fmt.Sprintf("  %-5s %s:%d", what, host, port)
	var b strings.Builder
	if r.OK {
		b.WriteString(stGood.Render(head+"   ok") + stDim.Render("  ·  "+short(r.Duration)) + "\n")
	} else {
		b.WriteString(stBad.Render(head+"   failed at "+string(r.Reached)) + "\n")
	}
	if r.Detail != "" {
		b.WriteString(indent(stDim.Render(wrapText(r.Detail, maxi(w-10, 24))), 8) + "\n")
	}
	return b.String()
}

func (v *accountsView) renderFolders(w int) string {
	var b strings.Builder
	b.WriteString("  " + stAccent.Render("The server's own folder names") + "\n\n")
	fields := []labelled{
		{"Sent", &v.fSent}, {"Drafts", &v.fDrafts}, {"Trash", &v.fTrash},
		{"Spam", &v.fSpam}, {"Archive", &v.fArchive},
	}
	for i, f := range fields {
		b.WriteString("  " + f.in.render(v.field == i) + "\n")
	}
	note := "Read from the server during the check, not guessed from a table — this is why the " +
		"Sent folder is spelled the way that server spells it."
	if v.draft.Quirks.NoRecord {
		note += " This provider files sent mail itself, so no copy is saved from here."
	}
	b.WriteString("\n" + indent(stDim.Render(wrapText(note, maxi(w-6, 30))), 2) + "\n")
	b.WriteString("\n" + indent(stNote.Render("enter writes everything"), 2) + "\n")
	return b.String()
}

func (v *accountsView) renderApply(w, h int) string {
	var b strings.Builder
	b.WriteString("  " + stAccent.Render("What was written") + "\n\n")
	if v.busy != "" {
		b.WriteString("  " + stWarn.Render(v.busy+"…") + "\n")
		return b.String()
	}
	for _, l := range setup.Describe(v.plan) {
		st := stFg
		switch {
		case strings.HasPrefix(l, "✕"):
			st = stBad
		case strings.HasPrefix(l, "✓"):
			st = stGood
		}
		for _, wrapped := range hanging("  ", l, maxi(w-4, 30)) {
			b.WriteString(indent(st.Render(wrapped), 2) + "\n")
		}
	}
	if !v.plan.Failed() && len(v.plan.Steps) > 0 {
		b.WriteString("\n" + indent(stNote.Render("next:  mbsync "+v.draft.Address), 2) + "\n")
		b.WriteString(indent(stDim.Render(
			"mailforge does not fetch for you — how much mail comes down is your call."), 2) + "\n")
	}
	return b.String()
}

func (v *accountsView) renderDelete(w int) string {
	a, ok := v.current()
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString("  " + stBad.Render("Remove "+a.Address) + "\n\n")
	b.WriteString(indent(stDim.Render(wrapText(
		"The mbsync block, the rc file and the registry entry go. The two below do not, unless "+
			"you say so — deleting a record and deleting somebody's correspondence are different "+
			"acts and this program keeps them apart.", maxi(w-6, 30))), 2))
	b.WriteString("\n\n")

	box := func(on bool) string {
		if on {
			return stGood.Render("")
		}
		return stDim.Render("")
	}
	rows := []struct {
		on    bool
		label string
	}{
		{v.delPass, "also remove " + a.PassEntry(v.cfg) + " from pass"},
		{v.delMail, "also delete " + a.Maildir(v.cfg) + " — every message in it"},
	}
	for i, r := range rows {
		mark := "  "
		st := stRow
		if v.delFocus == i {
			mark = stKey.Render(pointer) + " "
			st = stRowSel
		}
		b.WriteString(mark + box(r.on) + " " + st.Render(r.label) + "\n")
	}
	return b.String()
}

func (v *accountsView) renderList(w, h int) string {
	var b strings.Builder
	head := ""
	if v.err != "" {
		head += indent(stBad.Render(wrapText(v.err, maxi(w-4, 30))), 2) + "\n\n"
	}
	if v.busy != "" {
		head += "  " + stWarn.Render(v.busy+"…") + "\n\n"
	}
	b.WriteString(head)
	if len(v.reg.Accounts) == 0 {
		b.WriteString(indent(stDim.Render(wrapText(
			"No accounts yet. [a] adds one: it asks for the address, works out where its servers "+
				"are, proves the password against them, and only then writes anything.",
			maxi(w-6, 30))), 2))
		return b.String()
	}

	header := stDim.Render(fmt.Sprintf("     %-30s %-22s %-22s %s",
		"address", "imap", "smtp", "verified")) + "\n"
	b.WriteString(header)

	// The paths under the list, built before the list is windowed: the budget is measured from
	// what this frame really draws, not reserved for the worst case — once the body is padded
	// to its height, an unused reserve is a band of blank rows between "… N below" and them.
	under := ""
	if a, ok := v.current(); ok {
		// Where the selected account actually lives — the question the whole program exists to
		// keep answerable, so it is on screen rather than derivable.
		var t strings.Builder
		t.WriteString("\n")
		for _, l := range []struct{ k, v string }{
			{"label", a.LabelOrDefault()},
			{"maildir", a.Maildir(v.cfg)},
			{"rc", a.RCFile(v.cfg)},
			{"pass", a.PassEntry(v.cfg)},
			{"channel", a.Channel()},
		} {
			for j, wrapped := range strings.Split(wrapText(l.v, maxi(w-16, 24)), "\n") {
				label := l.k
				if j > 0 {
					label = ""
				}
				t.WriteString("  " + cell(label, 10, stDim) + " " + stFg.Render(wrapped) + "\n")
			}
		}
		under = t.String()
	}

	start, end := markedWindow(len(v.reg.Accounts), v.sel, listBudget(h, head, header, under))
	if start > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("     … %d above", start)) + "\n")
	}
	for i, a := range v.reg.Accounts[start:end] {
		i += start
		mark := "  "
		row := stRow
		if i == v.sel {
			mark = stKey.Render(pointer) + " "
			row = stRowSel
		}
		bg := row.GetBackground()
		def := " "
		if a.Default {
			def = "*"
		}
		ver, verStyle := "never", stWarn.Background(bg)
		if !a.Verified.IsZero() {
			ver, verStyle = short(time.Since(a.Verified))+" ago", stDim.Background(bg)
		}
		b.WriteString(mark + stKey.Background(bg).Render(def) + " " +
			cell(tail(a.Address, 30), 30, row) + " " +
			cell(a.IMAP.Host, 22, stDim.Background(bg)) + " " +
			cell(a.SMTP.Host, 22, stDim.Background(bg)) + " " +
			cell(ver, maxi(w-82, 8), verStyle) + "\n")
	}
	if end < len(v.reg.Accounts) {
		b.WriteString(stDim.Render(fmt.Sprintf("     … %d below", len(v.reg.Accounts)-end)) + "\n")
	}

	b.WriteString(under)
	return b.String()
}

// verifyZero and verifyFail keep the two "nothing yet" and "could not even start" shapes in one
// place, so the render never has to ask whether a zero Result means untried or failed.
func verifyZero() verify.Result { return verify.Result{} }

func verifyFail(detail string) verify.Result {
	return verify.Result{Reached: verify.StageAuth, Detail: detail}
}
