package keys

import "testing"

// TestCanonicalCollapsesSpellings is the check that was missing when the fetch macro was put on
// \Cf while \006 — the same key — was four lines away on forget-passphrase.
func TestCanonicalCollapsesSpellings(t *testing.T) {
	same := [][2]string{
		{`\Cf`, `\006`},
		{`\cf`, `\006`},
		{`\Ci`, `\t`},
		{`\e`, `\033`},
		{`\033P`, `\eP`},
		{"<space>", " "},
		{"<Space>", " "},
		{"<enter>", `\n`},
	}
	for _, p := range same {
		if Canonical(p[0]) != Canonical(p[1]) {
			t.Errorf("%q and %q are the same key but canonicalise to %q and %q",
				p[0], p[1], Canonical(p[0]), Canonical(p[1]))
		}
	}

	// And things that are NOT the same key must stay apart, or the check trades one blindness
	// for another.
	diff := [][2]string{
		{`\Cf`, `\Cg`},
		{"gg", "g"},
		{"<up>", "<down>"},
		{`\033v`, `\033V`},
		{"J", "j"},
	}
	for _, p := range diff {
		if Canonical(p[0]) == Canonical(p[1]) {
			t.Errorf("%q and %q both canonicalise to %q", p[0], p[1], Canonical(p[0]))
		}
	}
}

// TestConflictsSeesThroughSpelling: the whole point of canonicalising is that this is reported.
func TestConflictsSeesThroughSpelling(t *testing.T) {
	m := Defaults()
	m["index.fetch-all"] = `\Cf`
	m["index.forget-passphrase"] = `\006`
	c := Conflicts(m)
	if len(c["index.fetch-all"]) == 0 || len(c["index.forget-passphrase"]) == 0 {
		t.Errorf("two spellings of Ctrl-F were not reported as one key: %v", c)
	}
}
