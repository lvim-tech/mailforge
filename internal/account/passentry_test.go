package account

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/config"
)

func layoutCfg(l config.PassLayout) config.Config {
	c := config.Default()
	c.PassPrefix = "mail"
	c.PassLayout = l
	return c
}

// TestPassEntryFollowsTheLayout: the same account has a different name in each layout, and the
// domain one is lowercased through Domain() — a store filed by hand should still be found when the
// address was typed with capitals.
func TestPassEntryFollowsTheLayout(t *testing.T) {
	a := Account{Address: "Hello@Example.COM"}
	for _, tc := range []struct {
		layout config.PassLayout
		want   string
	}{
		{config.LayoutFlat, "mail/Hello@Example.COM"},
		{config.LayoutDomain, "mail/example.com/Hello@Example.COM"},
	} {
		if got := a.PassEntry(layoutCfg(tc.layout)); got != tc.want {
			t.Errorf("PassEntry under %s = %q, want %q", tc.layout, got, tc.want)
		}
	}
}

// TestPassOverrideWins is the whole point of the field: a name that was chosen must not be
// re-derived, or the choice is made again by the next code to look at it.
func TestPassOverrideWins(t *testing.T) {
	const chosen = "somewhere/else/entirely"
	a := Account{Address: "hello@example.com", PassOverride: chosen}
	for _, l := range []config.PassLayout{config.LayoutFlat, config.LayoutDomain} {
		c := layoutCfg(l)
		if got := a.PassEntry(c); got != chosen {
			t.Errorf("PassEntry under %s = %q, want %q", l, got, chosen)
		}
		// And it is the ONLY candidate. Searching past a decision would reopen it.
		if got := a.PassCandidates(c); len(got) != 1 || got[0] != chosen {
			t.Errorf("PassCandidates under %s = %v, want exactly %q", l, got, chosen)
		}
	}
}

// TestPassCandidatesLeadWithTheConfiguredLayout: the fallback exists so a store filed the other way
// is found, but the configured layout must still be preferred — otherwise "pass_layout" would be a
// suggestion rather than a setting.
func TestPassCandidatesLeadWithTheConfiguredLayout(t *testing.T) {
	a := Account{Address: "hello@example.com"}
	for _, tc := range []struct {
		layout config.PassLayout
		want   []string
	}{
		{config.LayoutFlat, []string{"mail/hello@example.com", "mail/example.com/hello@example.com"}},
		{config.LayoutDomain, []string{"mail/example.com/hello@example.com", "mail/hello@example.com"}},
	} {
		got := a.PassCandidates(layoutCfg(tc.layout))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("PassCandidates under %s = %v, want %v", tc.layout, got, tc.want)
		}
	}
}

// TestPassEntryIgnoresAnEmptyOverride: an override trimmed to nothing is not a choice, and must
// fall back to the layout rather than producing an entry at the root of the store.
func TestPassEntryIgnoresAnEmptyOverride(t *testing.T) {
	c := layoutCfg(config.LayoutDomain)
	for _, override := range []string{"", "   ", "/", "//"} {
		a := Account{Address: "hello@example.com", PassOverride: override}
		if got, want := a.PassEntry(c), "mail/example.com/hello@example.com"; got != want {
			t.Errorf("PassEntry with override %q = %q, want %q", override, got, want)
		}
	}
}
