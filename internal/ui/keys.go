package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/keys"
	"github.com/lvim-tech/mailforge/internal/setup"
)

// keysView is the binding table: one row per function per menu, with the key it runs on.
//
// The key is TYPED rather than pressed. Pressing looks friendlier for a moment and then cannot
// express half of what muttrc accepts — `\Cn` is a control key a terminal reports as one byte,
// `gg` is two keystrokes that a capture would have to guess the end of, and `<F5>` is a name, not
// a character. Typing the binding is typing exactly what goes in the file, which is also the form
// every piece of NeoMutt documentation is written in.
//
// Six hundred rows need a FILTER, not better scrolling. Nobody walks that list looking for the one
// function they want; they know a word of its name. `/` narrows by name, by key and by NeoMutt's
// own description, so "delete", "\Cn" and "thread" all get somewhere.
type keysView struct {
	cfg   config.Config
	saved config.Config

	// menu is which menu's bindings are on screen: a tab, because a menu is a namespace and
	// six hundred rows in one list is a list nobody reads. Scrolling past a boundary told you
	// nothing about which side of it you were on; a tab is the boundary, always visible.
	menu int
	rows []keys.Action
	sel  int

	editing bool
	buf     input

	filtering bool
	filter    input
}

func newKeysView(c config.Config) *keysView {
	v := &keysView{
		cfg: c, saved: c,
		buf:    newInput("key", ""),
		filter: newInput("filter", "name, key or description"),
	}
	v.rebuild()
	return v
}

// rebuild lays the list out for the current filter.
func (v *keysView) rebuild() {
	want := strings.ToLower(strings.TrimSpace(v.filter.String()))
	v.rows = nil
	bound := v.bound()

	menu := v.menuName()
	for _, a := range keys.Actions() {
		if a.Menu != menu {
			continue
		}
		if want != "" && !matchesFilter(a, bound[a.Name()], want) {
			continue
		}
		v.rows = append(v.rows, a)
	}
	// Never past the end of a list that just got shorter under the cursor.
	if v.sel >= len(v.rows) {
		v.sel = len(v.rows) - 1
	}
	if v.sel < 0 {
		v.sel = 0
	}
}

// matchesFilter decides what the filter searches. The key is included because "what is on \Cn" is
// as natural a question as "where is delete", and the description because NeoMutt's own wording is
// often the only name a user has for a function.
func matchesFilter(a keys.Action, key, want string) bool {
	for _, s := range []string{a.Fn, a.Menu, key, a.Stock, a.Help} {
		if strings.Contains(strings.ToLower(s), want) {
			return true
		}
	}
	return false
}

// menuName is the menu being shown, guarded so a stale index cannot pick a menu that is gone.
func (v *keysView) menuName() string {
	m := keys.Menus()
	if v.menu < 0 || v.menu >= len(m) {
		return ""
	}
	return m[v.menu]
}

// switchMenu moves to another tab and starts at its top. The cursor is NOT carried across:
// row seventeen of the index and row seventeen of the pager have nothing to do with each other.
func (v *keysView) switchMenu(d int) {
	m := keys.Menus()
	v.menu = (v.menu + d + len(m)) % len(m)
	v.sel = 0
	v.rebuild()
}

func (v *keysView) Title() string { return "Keys" }

func (v *keysView) capturesInput() bool { return v.editing || v.filtering }

func (v *keysView) Init() tea.Cmd { return nil }

func (v *keysView) dirty() bool { return !config.Same(v.cfg, v.saved) }

// bound is the effective map, with every gap filled from the defaults.
func (v *keysView) bound() map[string]string { return keys.Fill(v.cfg.Keys) }

func (v *keysView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case reloadMsg:
		if v.dirty() {
			return v, nil
		}
		c, err := config.Load()
		if err != nil {
			return v, failure("%v", err)
		}
		v.cfg, v.saved = c, c
		v.rebuild()
		return v, nil

	case tea.KeyMsg:
		if v.filtering {
			switch msg.String() {
			case "esc":
				v.filter.set("")
				v.filtering = false
				v.rebuild()
				return v, nil
			case "enter", "down", "up":
				v.filtering = false
				return v, nil
			}
			v.filter.update(msg)
			v.rebuild()
			return v, nil
		}
		if v.editing {
			switch msg.String() {
			case "esc":
				v.editing = false
				return v, nil
			case "enter":
				if err := v.commit(v.buf.String()); err != nil {
					return v, failure("%v", err)
				}
				v.editing = false
				return v, nil
			}
			v.buf.update(msg)
			return v, nil
		}
		switch msg.String() {
		case "j", "down":
			v.move(1)
		case "k", "up":
			v.move(-1)
		case "l":
			v.switchMenu(1)
		case "h":
			v.switchMenu(-1)
		case "g":
			v.sel = 0
		case "G":
			v.sel = maxi(len(v.rows)-1, 0)
		case "/":
			v.filtering = true
			v.filter.seed(v.filter.String())
		case "esc":
			if v.filter.String() != "" {
				v.filter.set("")
				v.rebuild()
			}
		case "enter":
			if a, ok := v.current(); ok {
				v.editing = true
				v.buf.seed(v.bound()[a.Name()])
			}
		case "x":
			// Unbind. A separate key from clearing the field and pressing enter, because
			// that is exactly what somebody does by accident.
			if a, ok := v.current(); ok {
				v.set(a.Name(), "")
				return v, status("%s is now unbound", a.Name())
			}
		case "d":
			if a, ok := v.current(); ok {
				return v, v.restore(a, a.Key, "the key mailforge ships")
			}
		case "n":
			// Back to what NEOMUTT binds, which is not always mailforge's default — the two
			// differ exactly where a binding of the user's own took the key.
			if a, ok := v.current(); ok {
				return v, v.restore(a, a.Stock, "NeoMutt's own key")
			}
		case "D":
			v.cfg.Keys = keys.Defaults()
			return v, status("every binding back to its default")
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			// A straight jump. Six tabs is few enough to name and enough for stepping through
			// them to be the slow way; the digit is on the strip beside the name.
			if n := int(msg.String()[0] - '1'); n < len(keys.Menus()) {
				v.menu, v.sel = n, 0
				v.rebuild()
			}
		case "w":
			return v, v.save()
		case "r":
			if !v.dirty() {
				return v, nil
			}
			v.cfg = v.saved
			return v, status("changes dropped")
		}
	}
	return v, nil
}

// restore puts an action back on a key it came with, and names WHICH key that is.
//
// There are TWO defaults on this screen and the difference matters exactly where it is least
// obvious. For most rows they are the same key; for the ones a binding of the user's own took
// over, "default" is ambiguous — `[d]` on delete-message gives back D, which is what mailforge
// ships, while the D→d the row shows on its right is NeoMutt's and belongs to `[n]`. A message
// reading "back to D" for a press meant to produce d is indistinguishable from a broken key.
//
// It also says when NOTHING moved. A row already on the key asked for is the common case — most
// of six hundred are — and confirming a change that did not happen reads the same way.
func (v *keysView) restore(a keys.Action, key, what string) tea.Cmd {
	if v.bound()[a.Name()] == key {
		return status("%s is already on %s: %s", a.Fn, what, orNone(key))
	}
	v.set(a.Name(), key)
	return status("%s back to %s: %s", a.Fn, what, orNone(key))
}

func orNone(k string) string {
	if k == "" {
		return "nothing"
	}
	return k
}

func (v *keysView) move(d int) {
	if i := v.sel + d; i >= 0 && i < len(v.rows) {
		v.sel = i
	}
}

func (v *keysView) current() (keys.Action, bool) {
	if v.sel < 0 || v.sel >= len(v.rows) {
		return keys.Action{}, false
	}
	return v.rows[v.sel], true
}

func (v *keysView) set(name, key string) {
	if v.cfg.Keys == nil {
		v.cfg.Keys = keys.Defaults()
	} else {
		v.cfg.Keys = keys.Fill(v.cfg.Keys) // copy, so saved keeps its own map
	}
	v.cfg.Keys[name] = key
}

// commit validates a typed binding before it can reach the file.
//
// A key with a space in it renders as `bind index J K sidebar-next`, which NeoMutt reads as a
// binding of J to a function called K — it reports the unknown function at every start and the key
// does nothing, with no sign on this screen that anything is wrong.
func (v *keysView) commit(s string) error {
	a, ok := v.current()
	if !ok {
		return nil
	}
	s = strings.TrimSpace(s)
	if s == "" {
		v.set(a.Name(), "")
		return nil
	}
	if strings.ContainsAny(s, " \t\"") {
		return fmt.Errorf("a binding cannot contain a space or a quote — write \\Cn, <F5> or gg")
	}
	v.set(a.Name(), s)
	return nil
}

// save writes the config AND regenerates keys.rc.
//
// Both, because a settings file that has changed while the file NeoMutt reads has not is a
// half-applied state the user cannot see: the screen would show the new binding and the client
// would keep the old one. This tab is the one place where writing the config is meant to change
// behaviour immediately, so it does the render too.
func (v *keysView) save() tea.Cmd {
	if err := config.Save(v.cfg); err != nil {
		return failure("%v", err)
	}
	v.saved = v.cfg

	var p setup.Plan
	setup.ApplyKeys(v.cfg, &p)
	if p.Failed() {
		for _, s := range p.Steps {
			if s.Err != nil {
				return failure("%v", s.Err)
			}
		}
	}
	return status("written — restart neomutt to pick it up")
}

func (v *keysView) Render(w, h int) string {
	var b strings.Builder
	bound := v.bound()
	clashes := keys.Conflicts(bound)

	head := ""
	if v.filtering || v.filter.String() != "" {
		head += "  " + v.filter.render(v.filtering) + "\n"
		head += "  " + stDim.Render(fmt.Sprintf("%d of %d actions",
			len(v.rows), len(keys.Actions()))) + "\n\n"
	} else {
		head += "  " + stDim.Render("file: ") +
			stFg.Render(v.cfg.NeomuttDir+"/mailforge/keys.rc") + "\n"
		head += "  " + stDim.Render("your own bindings.rc is read after it, so anything there wins") + "\n\n"
	}

	// The tab strip and the column titles, drawn ONCE and OUTSIDE the scrolling region.
	//
	// The menu used to be a heading row inside the list, which put it on screen only while the
	// boundary it marked was on screen — scrolled past, it named nothing, and the menu is the
	// one thing that decides what a key here means: `h` is display-headers in the index and
	// exit in the pager. As a tab it is the boundary, and it is always visible.
	//
	// FOREGROUND ONLY. The shell's own tabs are filled strips; a second filled strip under them
	// competes for the same "you are here", and the eye has to work out which of the two is the
	// outer one. Colour without a fill reads as a level down.
	// Numbered, because the number IS the shortcut. A strip that can only be walked with h and
	// l makes the sixth menu five keypresses away from the first.
	var tabs []string
	for i, m := range keys.Menus() {
		st := stDim
		if i == v.menu {
			st = stTabInner
		}
		tabs = append(tabs, stDim.Render(fmt.Sprintf("%d ", i+1))+st.Render(m))
	}
	strip := "  " + strings.Join(tabs, stDim.Render(" · "))

	// Where the cursor is in THIS menu. A scrollbar would say the same thing less exactly, and
	// six hundred rows split six ways is a list whose length is worth knowing.
	pos := ""
	if len(v.rows) > 0 {
		pos = fmt.Sprintf("%d/%d", v.sel+1, len(v.rows))
	}
	head += strip + "   " + stDim.Render(pos) + "\n\n"

	head += "  " + cell("  function", 30, stHead) + " " +
		cell("key", 12, stHead) + " " +
		cell("neomutt", 16, stHead) + " " +
		cell("what it does", maxi(w-64, 8), stHead) + "\n"
	b.WriteString(head)

	// What goes UNDER the list — the note on the selected action and the changed line — built
	// before the list is windowed: the budget is measured from what this frame really draws,
	// not reserved for the worst case. The reserve stood on screen as blank rows between the
	// list and the note once the body was padded to its height.
	under := ""
	if a, ok := v.current(); ok {
		note := a.Name()
		if a.Help != "" {
			note += " — " + a.Help
		}
		if a.IsMacro() {
			note += "  (macro: " + a.Macro + ")"
		}
		if bound[a.Name()] != a.Stock {
			note += "  ·  NeoMutt binds it to " + orNone(a.Stock)
			if taken := heldBy(bound, a.Menu, a.Stock, a.Name()); taken != "" {
				note += ", which here is " + taken
			}
		}
		if others := clashes[a.Name()]; len(others) > 0 {
			// Named explicitly, because NeoMutt resolves this by keeping the last binding
			// it read and saying nothing — the other action simply stops working.
			note += fmt.Sprintf("  ⚠ same key as %s in this menu; only one of them will work",
				strings.Join(others, ", "))
		}
		under += "\n" + indent(stDim.Render(wrapText(note, maxi(w-6, 30))), 2) + "\n"
	}
	if v.dirty() {
		under += "\n  " + stWarn.Render("changed — [w] writes it, [r] drops it")
	}

	// No "… N above/below" markers here — the position indicator in the strip says the same
	// thing — so the plain window is the right one.
	start, end := window(len(v.rows), v.sel, listBudget(h, head, under))

	for i := start; i < end; i++ {
		a := v.rows[i]
		mark := "  "
		row := stRow
		if i == v.sel {
			mark = stKey.Render(pointer) + " "
			row = stRowSel
		}
		bg := row.GetBackground()

		if v.editing && i == v.sel {
			b.WriteString(mark + cell("  "+a.Fn, 30, row) + " " + v.buf.render(true) + "\n")
			continue
		}

		// Each column gets its own colour, and each colour is one already spoken here: BLUE is
		// a key you press — the same blue as every hint in the footer; PURPLE is what a thing
		// was before; YELLOW is what will bite you later; GREY is context. Four columns that
		// look alike are four columns the eye has to read in order, which on six hundred rows
		// is the difference between finding a binding and scrolling past it.
		key := bound[a.Name()]
		style := stKey.Background(bg)
		switch {
		case key == "":
			key, style = "(unbound)", stDim.Background(bg)
		case len(clashes[a.Name()]) > 0:
			style = stWarn.Background(bg)
		}
		// What NEOMUTT binds, shown only where it is not what this row is on. A column
		// repeating the same key on every line would be noise; one that appears exactly where a
		// key was taken over answers "what was this before, and who took it".
		origin := ""
		if bound[a.Name()] != a.Stock {
			origin = "neomutt " + orNone(a.Stock)
		}
		b.WriteString(mark + cell("  "+a.Fn, 30, row) + " " +
			cell(key, 12, style) + " " +
			cell(origin, 16, stAccent.Background(bg)) + " " +
			cell(a.Help, maxi(w-64, 8), stDim.Background(bg)) + "\n")
	}

	b.WriteString(under)
	return b.String()
}

// heldBy names whatever holds a key in a menu now, so "NeoMutt binds it to d" can go on to say
// what d does here instead. Without that the user is told a key was taken and not by what.
func heldBy(bound map[string]string, menu, key, except string) string {
	if key == "" {
		return ""
	}
	for _, a := range keys.Actions() {
		if a.Menu == menu && a.Name() != except && bound[a.Name()] == key {
			return a.Fn
		}
	}
	return ""
}

func (v *keysView) Footer() string {
	if v.filtering {
		return joinHints(hint("enter", "keep"), hint("esc", "clear"))
	}
	if v.editing {
		return joinHints(hint("enter", "accept"), hint("esc", "cancel"))
	}
	f := []string{hint("h/l", "menu"), hint("1-6", "jump"), hint("j/k", "move"), hint("/", "filter"),
		hint("enter", "rebind"), hint("x", "unbind"), hint("d", "shipped"), hint("n", "neomutt")}
	if v.dirty() {
		f = append(f, hint("w", "write"), hint("r", "revert"))
	}
	return joinHints(f...)
}
