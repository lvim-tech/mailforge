package config

import "testing"

// TestPassLayoutDefaultsToFlat: every store mailforge has written so far is flat, and a default
// that relocated them would be a migration nobody asked for. A domain-filed store is still found —
// by the fallback in the account package — without this having to change.
func TestPassLayoutDefaultsToFlat(t *testing.T) {
	if got := Default().PassLayout; got != LayoutFlat {
		t.Fatalf("Default().PassLayout = %q, want %q", got, LayoutFlat)
	}
}

// TestUnknownPassLayoutFallsBackRatherThanFailing. config.json is hand-editable, and a typo in one
// field must not stop mailforge from starting — the cost of refusing is a program that cannot be
// opened to fix the file that stopped it.
func TestUnknownPassLayoutFallsBackRatherThanFailing(t *testing.T) {
	for _, in := range []PassLayout{"", "FLAT", "domains", "nested", " domain"} {
		got := Config{PassLayout: in}.withDefaults().PassLayout
		if got != LayoutFlat {
			t.Errorf("withDefaults with %q left %q, want %q", in, got, LayoutFlat)
		}
	}
}

// TestKnownPassLayoutSurvivesNormalisation — the mirror of the test above, and the one that would
// catch a fallback written so eagerly that it overwrote a valid setting.
func TestKnownPassLayoutSurvivesNormalisation(t *testing.T) {
	got := Config{PassLayout: LayoutDomain}.withDefaults().PassLayout
	if got != LayoutDomain {
		t.Fatalf("withDefaults turned %q into %q", LayoutDomain, got)
	}
}

// TestPassLayoutOtherPairsTheTwo: the fallback search and the duplicate check are both "look at the
// other one", so a broken Other() would silently make them look at the same entry twice.
func TestPassLayoutOtherPairsTheTwo(t *testing.T) {
	if got := LayoutFlat.Other(); got != LayoutDomain {
		t.Errorf("LayoutFlat.Other() = %q, want %q", got, LayoutDomain)
	}
	if got := LayoutDomain.Other(); got != LayoutFlat {
		t.Errorf("LayoutDomain.Other() = %q, want %q", got, LayoutFlat)
	}
}

// TestAbsentAndEmptyKeepHeadersDiffer is the distinction the field exists to preserve: a config
// with no opinion gets the default list, and one that deliberately asked for no weeding keeps that
// answer instead of having it filled back in.
func TestAbsentAndEmptyKeepHeadersDiffer(t *testing.T) {
	absent := Config{}.withDefaults().KeepHeaders
	if len(absent) == 0 {
		t.Error("an absent keep_headers should be filled from the default, not left empty")
	}
	empty := Config{KeepHeaders: []string{}}.withDefaults().KeepHeaders
	if len(empty) != 0 {
		t.Errorf("an explicitly empty keep_headers was overwritten with %v", empty)
	}
}
