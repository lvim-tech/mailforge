package theme

import "testing"

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

// TestSwatchLineReadsThePluginsIndex against a real line of extras/themes.txt, whose columns are
// fixed by the generator that writes it: name bg fg red orange yellow green teal cyan blue purple
// magenta.
func TestSwatchLineReadsThePluginsIndex(t *testing.T) {
	const index = "LvimBase_dark #20272b #5e8888 #d75e52 #e19556 #d8b36f #7e965a #407e70 #448595 #447ca2 #40719a #c6614d\n" +
		"LvimEverforest_soft #292f33 #5a6158 #cb4f4f #cc7942 #af9e6b #75783a #357b6d #527a57 #42728b #635d71 #bb755e\n"

	r, ok := swatchLine(index, "LvimBase_dark")
	if !ok {
		t.Fatal("the theme was not found in the index")
	}
	p := r.Palette
	if p.BG != "#20272b" || p.FG != "#5e8888" || p.Red != "#d75e52" ||
		p.Orange != "#e19556" || p.Yellow != "#d8b36f" || p.Green != "#7e965a" ||
		p.Blue != "#447ca2" || p.Purple != "#40719a" {
		t.Errorf("the columns were read in the wrong order: %+v", p)
	}
	// The two roles the index does not carry are derived, not left at the built-in's values.
	if p.BGAlt == Builtin().BGAlt || p.Dim == Builtin().Dim {
		t.Errorf("bg_alt and dim were not derived: %q %q", p.BGAlt, p.Dim)
	}
	// everforest_soft is the case the readability floor exists for: its editor `fg` is #5a6158 on
	// a #292f33 background, which is 1.6:1 as plain text.
	soft, _ := swatchLine(index, "LvimEverforest_soft")
	if c := contrast(soft.Palette.FG, soft.Palette.BG); c < 3 {
		t.Errorf("everforest_soft's foreground reads at %.1f:1", c)
	}
	if soft.Palette.FG == "#5a6158" {
		t.Error("the muted editor foreground was taken as plain text unchanged")
	}
	if _, ok := swatchLine(index, "LvimNothing_dark"); ok {
		t.Error("a theme that is not in the index was found anyway")
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
