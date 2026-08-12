// Package keys is the table of what a key can do in NeoMutt, and what it does by default.
//
// It sits below both config and render on purpose. The config stores only name → key, because a
// settings file that carried `bind index J sidebar-next` would be a muttrc with extra steps — and
// then two files would have opinions about NeoMutt's syntax. The knowledge of which function a name
// means, and which menu it belongs to, lives here and nowhere else.
//
// ONE ROW PER MENU, because that is what NeoMutt actually has. `h` is display-headers in the index
// and exit in the pager; `l` opens a message in one menu and an attachment in another. A model with
// one key spanning several menus cannot say that, and the first function whose stock key differs
// between two menus would have to be misrepresented in one of them.
//
// The table itself is GENERATED from the NeoMutt source — see table.go — and then probed against
// the installed binary, so every name in it is one this NeoMutt answers to. What is hand-written
// here is only the part no source file knows: which of those keys the user had already made their
// own, kept exactly as they were.
package keys

import "sort"

// Action is one thing a key can do, in one menu.
type Action struct {
	// Menu is the NeoMutt menu this binds in — exactly one.
	Menu string
	// Fn is the NeoMutt function. For a macro it is the name mailforge gives the action, since
	// a macro answers to no function at all.
	Fn string
	// Macro is a key sequence, for what no single function does. Empty for a plain binding.
	Macro string
	// Key is what mailforge binds it to out of the box: NeoMutt's own key, unless the user had
	// already taken it for something else.
	Key string
	// Stock is the key NeoMutt itself binds this function to, kept even when Key differs — the
	// interface shows both, so a row that was taken over says what it was taken from.
	Stock string
	// Help is NeoMutt's own one-line description, from its opcode table.
	Help string
}

// Name is the stable id: the key in config.json, and what the interface lists.
func (a Action) Name() string { return a.Menu + "." + a.Fn }

// IsMacro reports whether this action is a macro rather than a plain binding.
func (a Action) IsMacro() bool { return a.Macro != "" }

// Overridden reports whether mailforge ships this on a different key than NeoMutt does.
func (a Action) Overridden() bool { return a.Key != a.Stock }

// Actions returns the table in display order: by menu, then by function name.
func Actions() []Action {
	out := make([]Action, len(table))
	copy(out, table)
	return out
}

// Menus returns the menu names in the order they appear, for the interface to band by.
func Menus() []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range table {
		if !seen[a.Menu] {
			seen[a.Menu] = true
			out = append(out, a.Menu)
		}
	}
	return out
}

// Find looks an action up by name.
func Find(name string) (Action, bool) {
	for _, a := range table {
		if a.Name() == name {
			return a, true
		}
	}
	return Action{}, false
}

// Defaults returns the binding for each action on a machine that has not said otherwise.
func Defaults() map[string]string {
	out := make(map[string]string, len(table))
	for _, a := range table {
		out[a.Name()] = a.Key
	}
	return out
}

// Default returns one action's default binding.
func Default(name string) string {
	a, _ := Find(name)
	return a.Key
}

// Fill completes a map read from disk.
//
// Per-action rather than all-or-nothing: a config written by an older build knows nothing about an
// action added later, and the alternative to filling each gap is a user whose careful bindings are
// discarded wholesale because one name was missing. Entries for actions that no longer exist are
// dropped, so a stale name cannot sit in the file forever looking like it does something.
func Fill(m map[string]string) map[string]string {
	out := Defaults()
	for name, k := range m {
		if _, ok := Find(name); !ok {
			continue
		}
		out[name] = k // including "", which means deliberately unbound
	}
	return out
}

// Conflicts reports keys bound to more than one action within the same menu.
//
// This is the mistake the interface cannot let pass silently: NeoMutt takes the LAST binding it
// reads and says nothing, so the earlier action simply stops working, and the file that caused it
// looks perfectly reasonable. Two actions may share a key when they are in different menus — `l`
// opens a message in the index and an attachment in the attach menu, and that is not a clash.
func Conflicts(m map[string]string) map[string][]string {
	type slot struct{ menu, key string }
	hits := map[slot][]string{}

	for _, a := range table {
		k := m[a.Name()]
		if k == "" {
			continue
		}
		// CANONICAL, not as written: `\Cf` and `\006` are the same key in two spellings, and a
		// comparison of the spellings calls that pair free.
		s := slot{a.Menu, Canonical(k)}
		hits[s] = append(hits[s], a.Name())
	}

	out := map[string][]string{}
	for _, names := range hits {
		if len(names) < 2 {
			continue
		}
		for _, n := range names {
			for _, other := range names {
				if other != n && !contains(out[n], other) {
					out[n] = append(out[n], other)
				}
			}
		}
	}
	for n := range out {
		sort.Strings(out[n])
	}
	return out
}

// StockKeys reports every key NeoMutt binds in a menu, alternates included.
//
// Separate from the table because the table holds ONE key per function — what a settings screen
// shows — while this answers a different question: is a key free at all. display-message alone
// answers to <space>, <keypadenter>, \n and \r, so a macro placed on the second of those looks
// unused in every row on screen while taking a key the client already has.
func StockKeys(menu string) []string { return stockKeys[menu] }

// FreeIn reports whether NeoMutt leaves a key unbound in a menu, comparing canonically.
func FreeIn(menu, key string) bool {
	want := Canonical(key)
	for _, k := range stockKeys[menu] {
		if Canonical(k) == want {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
