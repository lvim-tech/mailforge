package render

import (
	"strings"
	"testing"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/keys"
)

func TestKeysRC(t *testing.T) {
	c := config.Default()
	got := KeysRC(c)

	for _, want := range []string{
		`bind index "d" delete-message`,
		`bind pager "q" exit`,
		`bind index "l" limit`,
		`macro index "\Cy" "<shell-escape>mailforge fetch >/dev/null 2>&1<enter><check-stats>" "run mbsync for every account"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing line:\n  %s", want)
		}
	}

	// Every non-comment line is a bind or a macro, and its key is quoted. A stray line here is
	// one NeoMutt refuses with an error banner at every start, and a binding that does nothing.
	for _, l := range strings.Split(got, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if f[0] != "bind" && f[0] != "macro" {
			t.Errorf("unexpected line: %q", l)
			continue
		}
		if !strings.HasPrefix(f[2], `"`) {
			t.Errorf("unquoted key: %q", l)
		}
	}
}

// TestKeysRCQuotesTheAwkwardKeys is the case that cost a round of "too few arguments": `#` opens a
// comment in muttrc and `;` ends the command, so those two lines arrive truncated when bare.
func TestKeysRCQuotesTheAwkwardKeys(t *testing.T) {
	got := KeysRC(config.Default())
	for _, want := range []string{
		`bind index "#" break-thread`,
		`bind index ";" tag-prefix`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing or unquoted:\n  %s", want)
		}
	}
}

// TestKeysRCIsStable: apply compares bytes to decide whether to write at all, so a renderer whose
// output depends on map iteration order would rewrite the file on every run and report a change
// that never happened.
func TestKeysRCIsStable(t *testing.T) {
	c := config.Default()
	first := KeysRC(c)
	for i := 0; i < 20; i++ {
		if KeysRC(c) != first {
			t.Fatalf("output changed between two calls with the same input")
		}
	}
}

// TestKeysRCOmitsUnbound: an empty binding means the user took the key away on purpose, and the
// file has to say nothing rather than emit `bind index "" delete-message`.
func TestKeysRCOmitsUnbound(t *testing.T) {
	c := config.Default()
	c.Keys = keys.Fill(map[string]string{"index.delete-message": ""})
	got := KeysRC(c)
	// The whole line, not the function name: "undelete-message" contains "delete-message", and
	// a substring check here passes or fails for the wrong reason.
	if strings.Contains(got, `bind index "d" delete-message`) {
		t.Error("an unbound action was still written")
	}
	if !strings.Contains(got, `bind index "u" undelete-message`) {
		t.Error("unbinding one action removed another")
	}
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "bind ") && len(strings.Fields(l)) != 4 {
			t.Errorf("malformed line: %q", l)
		}
	}
}

// TestKeysRCHonoursARebind: the point of the whole tab.
func TestKeysRCHonoursARebind(t *testing.T) {
	c := config.Default()
	c.Keys = keys.Fill(map[string]string{"index.delete-message": `\Cn`})
	got := KeysRC(c)
	if !strings.Contains(got, `bind index "\Cn" delete-message`) {
		t.Error("the rebinding did not reach the file")
	}
	if strings.Contains(got, `bind index "d" delete-message`) {
		t.Error("the old binding was written as well")
	}
	// A row in another menu is a separate action now, and an index rebind must not touch it.
	if !strings.Contains(got, `bind pager "q" exit`) {
		t.Error("rebinding one menu changed another")
	}
}
