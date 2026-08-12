package keys

import "testing"

// TestTheTableIsWellFormed guards the generated file: it is machine-written from the NeoMutt
// source, and a change in that source's shape would show up here rather than as a muttrc NeoMutt
// complains about at somebody's next start.
func TestTheTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Actions() {
		if a.Menu == "" {
			t.Errorf("%q binds in no menu", a.Fn)
		}
		if a.Fn == "" {
			t.Errorf("an action in %s has no name", a.Menu)
		}
		if seen[a.Name()] {
			t.Errorf("%s appears twice", a.Name())
		}
		seen[a.Name()] = true
		// A key with a space in it renders as `bind index J K fn`, which NeoMutt reads as a
		// binding of J to a function called K.
		for _, k := range []string{a.Key, a.Stock} {
			if k != k2(k) {
				t.Errorf("%s has an unusable key %q", a.Name(), k)
			}
		}
	}
	if len(seen) < 500 {
		t.Errorf("only %d actions — the generator produced a fraction of the table", len(seen))
	}
}

func k2(s string) string {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '"' {
			return ""
		}
	}
	return s
}

// TestTheDefaultsAreClean: the program must not ship with a warning on screen, and a clash among
// its own defaults is one NeoMutt would resolve by silently dropping a binding.
func TestTheDefaultsAreClean(t *testing.T) {
	if c := Conflicts(Defaults()); len(c) != 0 {
		t.Errorf("the default bindings clash with each other: %v", c)
	}
}

// TestTheShippedKeysAreNeoMuttsOwn — every row on the key the client itself binds.
//
// The generator can override any of them (see OVERRIDES in gen_keys.py) and the machinery for that
// stays. What is asserted here is the SHIPPED state: no overrides, so what NeoMutt's manual
// describes is what this configuration does.
func TestTheShippedKeysAreNeoMuttsOwn(t *testing.T) {
	// The sidebar is the one exception, and it is not a preference: NeoMutt ships the sidebar
	// with no bindings at all, so "NeoMutt's own key" for these is no key. The four below are
	// mailforge's, and the three stock functions whose keys they take are checked to have been
	// released rather than left to collide.
	sidebar := map[string]bool{
		"sidebar-next": true, "sidebar-prev": true,
		"sidebar-open": true, "sidebar-toggle-visible": true,
	}
	for _, a := range Actions() {
		if a.IsMacro() || sidebar[a.Fn] {
			continue
		}
		if a.Key != a.Stock {
			// Displaced by a sidebar binding: the key must be GIVEN UP, not moved elsewhere.
			if a.Key == "" {
				continue
			}
			t.Errorf("%s ships on %q while NeoMutt binds it to %q", a.Name(), a.Key, a.Stock)
		}
	}
	for fn := range sidebar {
		for _, m := range []string{"index", "pager"} {
			a, ok := Find(m + "." + fn)
			if !ok || a.Key == "" {
				t.Errorf("%s.%s has no key", m, fn)
			}
			if a.Stock != "" {
				t.Errorf("%s.%s claims a NeoMutt key %q; NeoMutt binds no sidebar key", m, fn, a.Stock)
			}
		}
	}
}

// TestTheMacrosAreBoundAndClashWithNothing: the two macros are mailforge's own — no NeoMutt
// function does what they do — so their keys are the one place this table invents anything, and
// the one place a clash with a stock binding could appear unnoticed.
func TestTheMacrosAreBoundAndClashWithNothing(t *testing.T) {
	c := Conflicts(Defaults())
	for _, name := range []string{"index.fetch-all", "pager.fetch-all", "browser.parent-dir"} {
		a, ok := Find(name)
		if !ok {
			t.Fatalf("%s is missing", name)
		}
		if !a.IsMacro() || a.Key == "" {
			t.Errorf("%s is not a bound macro: %+v", name, a)
		}
		if len(c[name]) > 0 {
			t.Errorf("%s sits on a key NeoMutt already uses here: %v", name, c[name])
		}
	}
}

// TestNoTwoRowsShareAKeyInAMenu is the rule an override has to respect: a key claimed by one row
// is taken away from whatever held it. Checked against the data, so it holds whether the table was
// generated with overrides or without.
func TestNoTwoRowsShareAKeyInAMenu(t *testing.T) {
	seen := map[string]string{}
	for _, a := range Actions() {
		if a.Key == "" {
			continue
		}
		id := a.Menu + "\x00" + Canonical(a.Key)
		if prev, dup := seen[id]; dup {
			t.Errorf("%s and %s both hold %q in %s", prev, a.Fn, a.Key, a.Menu)
		}
		seen[id] = a.Fn
	}
}

// TestFillKeepsChoicesAndAddsGaps is the whole reason Fill is per-action.
//
// A config written before an action existed knows nothing about it. Replacing the map wholesale
// when one name is missing would discard every binding the user had chosen, and they would find
// out by pressing a key.
func TestFillKeepsChoicesAndAddsGaps(t *testing.T) {
	got := Fill(map[string]string{"index.delete-message": "X"})
	if got["index.delete-message"] != "X" {
		t.Errorf("a chosen binding was lost: %q", got["index.delete-message"])
	}
	if got["index.limit"] != Default("index.limit") {
		t.Errorf("a missing binding was not filled: %q", got["index.limit"])
	}

	// An empty string is a DELIBERATE unbinding and must survive; only absence is a gap.
	if got := Fill(map[string]string{"index.delete-message": ""}); got["index.delete-message"] != "" {
		t.Errorf("an unbound action was re-bound to %q", got["index.delete-message"])
	}

	// A name from an older build is dropped rather than carried forever. This one matters more
	// than usual: the ids used to have no menu in them, so every entry an older config holds
	// looks exactly like this.
	if got := Fill(map[string]string{"sidebar-next": "Z"}); got["sidebar-next"] != "" {
		t.Errorf("a stale name survived Fill")
	}

	// Fill must not hand back the caller's map, or editing one screen would edit another.
	in := map[string]string{"index.delete-message": "X"}
	Fill(in)["index.delete-message"] = "Y"
	if in["index.delete-message"] != "X" {
		t.Errorf("Fill returned an alias of its input")
	}
}

// TestConflictsOnlyWithinAMenu: NeoMutt binds per menu, so the same key in two menus is two
// different keys. Reporting those as a clash would train the user to ignore the warning — and the
// real clash, where one action silently stops working, is exactly the thing they would then miss.
func TestConflictsOnlyWithinAMenu(t *testing.T) {
	// `l` opens a message in the index and an attachment in the attach menu. Not a clash.
	c := Conflicts(Fill(nil))
	if len(c["index.display-message"]) != 0 || len(c["attach.view-mailcap"]) != 0 {
		t.Errorf("two menus were treated as one: %v", c)
	}

	// Two actions in the same menu, same key: a real clash, reported for both.
	m := Defaults()
	m["index.limit"] = m["index.delete-message"]
	c = Conflicts(m)
	if len(c["index.limit"]) == 0 || len(c["index.delete-message"]) == 0 {
		t.Errorf("a genuine clash in one menu was not reported: %v", c)
	}

	// An unbound action clashes with nothing, however many others are unbound.
	m = Defaults()
	for name := range m {
		m[name] = ""
	}
	if c := Conflicts(m); len(c) != 0 {
		t.Errorf("unbound actions were reported as clashing: %v", c)
	}
}

// TestTheMacrosSitOnKeysNeoMuttLeavesFree closes the hole that put the fetch macro on Ctrl-F.
//
// The row-by-row check cannot see this: the table keeps one key per function, so `\006` on
// forget-passphrase was visible but `\n` on display-message was not, and either would have been
// taken silently. This asks the full list of keys the client binds in that menu, canonically.
func TestTheMacrosSitOnKeysNeoMuttLeavesFree(t *testing.T) {
	for _, a := range Actions() {
		if !a.IsMacro() || a.Key == "" {
			continue
		}
		if !FreeIn(a.Menu, a.Key) {
			t.Errorf("%s is on %q, which NeoMutt already binds in %s", a.Name(), a.Key, a.Menu)
		}
	}
}

// TestFreeInSeesThroughSpelling: the answer must not depend on how the key was written.
func TestFreeInSeesThroughSpelling(t *testing.T) {
	// Ctrl-F is forget-passphrase in the index, whichever way it is spelled.
	for _, k := range []string{`\Cf`, `\006`, `\cf`} {
		if FreeIn("index", k) {
			t.Errorf("%q reported free in the index", k)
		}
	}
	// And Enter, which no row of the table shows as its own key.
	for _, k := range []string{`\n`, "<enter>"} {
		if FreeIn("index", k) {
			t.Errorf("%q reported free in the index", k)
		}
	}
	if !FreeIn("index", `\Cy`) {
		t.Error(`\Cy reported taken in the index`)
	}
}
