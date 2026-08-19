package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtrasNameMatchesThePluginsOwnSpelling: the same theme is written two ways — the
// :colorscheme command in Neovim, the generated file name in extras/ — and mailforge has to find a
// palette whichever of the two the user has in hand.
func TestExtrasNameMatchesThePluginsOwnSpelling(t *testing.T) {
	cases := map[string]string{
		"lvim-everforest-soft": "LvimEverforest_soft",
		"lvim-tokyonight-dark": "LvimTokyonight_dark",
		"lvim-darker":          "LvimBase_darker", // the unprefixed family is called base
		"lvim-soft":            "LvimBase_soft",
		"LvimEverforest_soft":  "", // already the generated spelling: nothing to translate
		"solarized":            "",
	}
	for in, want := range cases {
		if got := extrasName(in); got != want {
			t.Errorf("extrasName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPaletteFillsMissingRolesFromTheBuiltin: a theme that names three colours is three colours
// changed and not a broken screen, and it SAYS which ones it did not supply.
func TestPaletteFillsMissingRolesFromTheBuiltin(t *testing.T) {
	r, ok := parsePalette([]byte(`{"name":"partial","colors":{"red":"#ff0000","bg":"#101010"}}`), "file")
	if !ok {
		t.Fatal("a partial palette was refused")
	}
	if r.Palette.Red != "#ff0000" || r.Palette.BG != "#101010" {
		t.Errorf("the supplied roles did not survive: %+v", r.Palette)
	}
	if r.Palette.Green != Builtin().Green {
		t.Errorf("green should have come from the built-in, got %q", r.Palette.Green)
	}
	if len(r.Notes) == 0 {
		t.Error("a palette completed from the built-in said nothing about it")
	}
}

// TestPaletteAcceptsTheHandWrittenShape: role → hex at the top level, which is what somebody
// writing one by hand produces before reading any documentation.
func TestPaletteAcceptsTheHandWrittenShape(t *testing.T) {
	r, ok := parsePalette([]byte(`{"bg":"101010","fg":"#EEEEEE"}`), "handmade")
	if !ok {
		t.Fatal("the flat shape was refused")
	}
	if r.Name != "handmade" {
		t.Errorf("a document with no name takes the file's: got %q", r.Name)
	}
	// Both spellings of a colour are accepted and normalised to one.
	if r.Palette.BG != "#101010" || r.Palette.FG != "#eeeeee" {
		t.Errorf("got %+v", r.Palette)
	}
}

// TestPaletteRefusesADocumentThatIsNotOne: a JSON file that happens to be somewhere mailforge
// looked must not be accepted as a theme — it would repaint with the built-in while the interface
// claimed to be showing that file's palette.
func TestPaletteRefusesADocumentThatIsNotOne(t *testing.T) {
	for _, doc := range []string{`{"colorscheme":"lvim-everforest-soft"}`, `{}`, `not json at all`} {
		if _, ok := parsePalette([]byte(doc), "x"); ok {
			t.Errorf("accepted %q as a palette", doc)
		}
	}
}

// TestUnreadableForegroundIsLifted: the colours here carry meaning, so a palette whose text is
// invisible on its own background is not a matter of taste. The hues are left alone; only the
// foreground moves, and it moves to whichever end the background can carry.
func TestUnreadableForegroundIsLifted(t *testing.T) {
	r, ok := parsePalette([]byte(`{"bg":"#101010","fg":"#151515","red":"#ff0000"}`), "unreadable")
	if !ok {
		t.Fatal("refused")
	}
	if c := contrast(r.Palette.FG, r.Palette.BG); c < 3 {
		t.Errorf("the foreground still reads at %.1f:1 (%q on %q)", c, r.Palette.FG, r.Palette.BG)
	}
	if r.Palette.Red != "#ff0000" {
		t.Errorf("a hue was changed: %q", r.Palette.Red)
	}
	// A palette that is already legible is left exactly as it was.
	r, _ = parsePalette([]byte(`{"bg":"#101010","fg":"#dddddd"}`), "fine")
	if r.Palette.FG != "#dddddd" {
		t.Errorf("a readable foreground was changed to %q", r.Palette.FG)
	}
}

// TestResolveReadsThePerAppYAML: a named theme is resolved to one per-app YAML document in the
// theme directory — the shape the plugin generates into extras/mailforge/*.yaml, all ten roles
// present. Both the generated spelling and the :colorscheme spelling find the same file.
func TestResolveReadsThePerAppYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	themes := filepath.Join(dir, "mailforge", "themes")
	if err := os.MkdirAll(themes, 0o755); err != nil {
		t.Fatal(err)
	}
	const doc = `name: LvimNord_dark
colors:
    bg: "#232831"
    bg_alt: "#2c313a"
    fg: "#b3bac6"
    dim: "#727883"
    red: "#ac6e74"
    yellow: "#c3a870"
    green: "#879978"
    blue: "#73889e"
    purple: "#887f93"
    orange: "#b88473"
`
	if err := os.WriteFile(filepath.Join(themes, "LvimNord_dark.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	// The generated spelling, and the :colorscheme spelling that translates to it, both land on the
	// same file — and the palette arrives exactly as written, since every role is supplied.
	for _, name := range []string{"LvimNord_dark", "lvim-nord-dark"} {
		r, ok := resolve(name)
		if !ok {
			t.Fatalf("resolve(%q) found no palette", name)
		}
		p := r.Palette
		if p.BG != "#232831" || p.BGAlt != "#2c313a" || p.FG != "#b3bac6" || p.Dim != "#727883" ||
			p.Red != "#ac6e74" || p.Yellow != "#c3a870" || p.Green != "#879978" ||
			p.Blue != "#73889e" || p.Purple != "#887f93" || p.Orange != "#b88473" {
			t.Errorf("resolve(%q) palette not read verbatim: %+v", name, p)
		}
		if r.Name != "LvimNord_dark" {
			t.Errorf("resolve(%q) name = %q", name, r.Name)
		}
	}

	// A literal path to the file is taken as given, wherever it is kept.
	if r, ok := resolve(filepath.Join(themes, "LvimNord_dark.yaml")); !ok || r.Palette.BG != "#232831" {
		t.Errorf("a literal .yaml path was not read: %v %+v", ok, r.Palette)
	}

	// A theme with no file is not resolved — the cascade moves on to the next source.
	if _, ok := resolve("LvimNothing_dark"); ok {
		t.Error("a theme with no file was resolved anyway")
	}
}

func TestNormaliseRefusesWhatLipglossCannotPaint(t *testing.T) {
	for _, bad := range []string{"", "#12345", "#gggggg", "red", "#1234567"} {
		if _, ok := normalise(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if got, ok := normalise("  #AABBCC "); !ok || got != "#aabbcc" {
		t.Errorf("got %q %v", got, ok)
	}
}
