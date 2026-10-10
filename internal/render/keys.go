package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/keys"
)

// KeysRC renders the bindings mailforge manages.
//
// A file of its own, NOT the user's bindings.rc. The two are different things: what this program
// manages has to be rewritable on every apply, and what the user writes by hand has to be safe from
// exactly that. Mixing them would mean either overwriting their work or never being able to change
// a binding from the interface, and neither is acceptable.
//
// Sourced BEFORE bindings.rc — see NeomuttKeysInclude for why that ordering is the whole design.
//
// EVERY bound action is written out, including the ones sitting on NeoMutt's own key. Emitting only
// the differences would make the file shorter and the Keys tab a liar: NeoMutt's defaults move
// between releases, and a tab that showed a remembered default while the client used a new one
// would be wrong in the one place the user went to check.
func KeysRC(c config.Config) string {
	var b strings.Builder
	b.WriteString(HeaderFromConfig)
	b.WriteString("#\n")
	b.WriteString("# Change these in mailforge, in the Keys tab. Anything you bind in your own\n")
	b.WriteString("# bindings.rc is read after this file and therefore wins.\n")

	bound := keys.Fill(c.Keys)

	// Grouped by menu, and each group in a fixed order, so two applies of the same configuration
	// produce identical bytes — that is what lets apply say "already up to date" and mean it.
	byMenu := map[string][]keys.Action{}
	for _, a := range keys.Actions() {
		if strings.TrimSpace(bound[a.Name()]) == "" {
			continue // deliberately unbound
		}
		byMenu[a.Menu] = append(byMenu[a.Menu], a)
	}
	menus := make([]string, 0, len(byMenu))
	for m := range byMenu {
		menus = append(menus, m)
	}
	sort.Strings(menus)

	for _, m := range menus {
		fmt.Fprintf(&b, "\n# %s\n", m)
		rows := byMenu[m]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Fn < rows[j].Fn })
		for _, a := range rows {
			k := quoteKey(bound[a.Name()])
			if a.IsMacro() {
				fmt.Fprintf(&b, "macro %s %s %s %s\n", m, k, quote(a.Macro), quote(a.Help))
				continue
			}
			fmt.Fprintf(&b, "bind %s %s %s\n", m, k, a.Fn)
		}
	}

	b.WriteString(sidebarKeys(c))
	return b.String()
}

// quoteKey wraps a key for a bind line, and is NOT the general quote.
//
// Two reasons it has to be its own thing. Quoted at all, because `#` opens a comment in muttrc and
// `;` ends the command — `bind index # break-thread` arrives as `bind index` and is refused for too
// few arguments. And WITHOUT escaping the backslash, because a key is written in NeoMutt's own
// escape language: `\Cn` is Ctrl-N and `\033P` is Alt-P, so doubling the backslash the way the
// general quote does turns both into a literal backslash followed by letters — a binding that
// parses, loads without complaint, and answers to a key nobody can press.
func quoteKey(k string) string {
	return `"` + strings.ReplaceAll(k, `"`, `\"`) + `"`
}

/*
sidebarKeys is the fold key, written after the table and only when the sidebar is folded.

NOT a row in the keys table, and the reason is structural rather than tidy: every row there is one
NeoMutt function in one menu, while this is a macro whose body names a generated file and runs a
program. The table is generated from NeoMutt's own source so that no name in it is invented; a row
for something NeoMutt has no function for would break exactly that guarantee.

\n AND \r, because that is what Enter is here. NeoMutt's own index and pager bind both for the
functions that open a message — they are in stockKeys, taken from its source — and a terminal sends
one or the other depending on how it was set up.

The prettier-looking <enter> is not used in their place, and the reason is what cannot be checked
rather than what can: NeoMutt accepts an unknown <name> as a literal key sequence WITHOUT
complaining, so a wrong name there gives a file that loads clean and a key that does nothing. The
pair above needs no such trust — NeoMutt's own tables name them.

The cost is stated plainly because it is the whole trade: Enter stops opening a message here.
<space> and <keypadenter> still do, and anybody who wants Enter back writes one bind in their own
bindings.rc, which NeoMutt reads after this file.
*/
func sidebarKeys(c config.Config) string {
	if !c.SidebarCollapse {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n# sidebar — Enter opens the highlighted row and folds that account.\n")
	b.WriteString("# It is NOT display-message any more; <space> and <keypadenter> open a message.\n")

	// THE INDEX ONLY, and not the pager. The macro opens the highlighted row before folding it,
	// which in the pager would mean Enter walking out of the message being read to a different
	// folder. In the pager Enter keeps whatever NeoMutt gives it, which is a line of scrolling.
	for _, k := range []string{`\n`, `\r`} {
		fmt.Fprintf(&b, "macro index %s %s %s\n", quoteKey(k),
			quote(SidebarToggle(c)), quote("open the highlighted row, and fold its account"))
	}
	return b.String()
}
