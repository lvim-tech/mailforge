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
