package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/render"
	"github.com/lvim-tech/mailforge/internal/setup"
)

// settingsView is a form over config.Config, and deliberately nothing more.
//
// Every field here decides where something GOES or how a rendered file reads. None of them is a
// secret and none belongs to an account — an account's server, login and folders are the other
// tab's business. Keeping that line sharp is what stops this screen from becoming the drawer a
// settings screen becomes when it has no test for belonging.
type settingsView struct {
	cfg   config.Config
	saved config.Config // what is on disk, for telling changed from not

	sel     int
	editing bool
	buf     input
}

type settingKind int

const (
	skText settingKind = iota
	skInt
	skBool
)

type setting struct {
	label string
	kind  settingKind
	// get returns the VALUE, and only the value. It is what seeds the editor when the row is
	// opened, so anything decorative in here is decoration the user is then asked to edit —
	// the first run of this screen turned "real name" into "(not set)Biser Stoilov", because
	// the placeholder was coming back from the same function the editor was filled from.
	get func(*settingsView) string
	// show renders the value for the table when it needs to read differently from what it is
	// — "all" for zero, "10 s" for ten. Absent means the value shows as itself.
	show func(*settingsView) string
	set  func(*settingsView, string) error
	// note is shown under the selected row: what the setting COSTS, not what it is. A label
	// that needs a sentence to say what it means is a badly named label.
	note string
}

var settings = []setting{
	{
		label: "maildir root",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.MaildirRoot },
		set:   func(v *settingsView, s string) error { return setPath(&v.cfg.MaildirRoot, s) },
		note: "one directory per address underneath. Changing it after accounts exist re-renders " +
			"to the new path but moves no mail — the doctor will say what was left behind.",
	},
	{
		label: "neomutt dir",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.NeomuttDir },
		set:   func(v *settingsView, s string) error { return setPath(&v.cfg.NeomuttDir, s) },
		note:  "neomuttrc lives here, and the mailforge-owned subtree beneath it.",
	},
	{
		label: "mbsyncrc",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.MbsyncRC },
		set:   func(v *settingsView, s string) error { return setPath(&v.cfg.MbsyncRC, s) },
		note:  "the one file mailforge edits without owning — always between markers.",
	},
	{
		label: "pass prefix",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.PassPrefix },
		set: func(v *settingsView, s string) error {
			s = strings.Trim(strings.TrimSpace(s), "/")
			if s == "" {
				return fmt.Errorf("a folder is required — passwords are not kept at the root of the store")
			}
			v.cfg.PassPrefix = s
			return nil
		},
		note: "account passwords become <prefix>/<address> in pass.",
	},
	{
		label: "pass layout",
		kind:  skText,
		get:   func(v *settingsView) string { return string(v.cfg.PassLayout) },
		set: func(v *settingsView, s string) error {
			l := config.PassLayout(strings.ToLower(strings.TrimSpace(s)))
			if !l.Valid() {
				return fmt.Errorf("%q is not a layout — use %q or %q",
					s, config.LayoutFlat, config.LayoutDomain)
			}
			v.cfg.PassLayout = l
			return nil
		},
		note: "flat = <prefix>/<address>; domain = <prefix>/<domain>/<address>, which is how " +
			"keyforge files a store. Either way the other is still searched, so nothing has to " +
			"move — this only says which name a NEW password is written under.",
	},
	{
		label: "kept headers",
		kind:  skText,
		get:   func(v *settingsView) string { return strings.Join(v.cfg.KeepHeaders, " ") },
		set: func(v *settingsView, s string) error {
			// Fields, not Split: the list is typed as a line, and Split on " " turns a double
			// space into an empty pattern — which NeoMutt matches against everything and which
			// would silently undo the weeding this setting exists to do.
			f := strings.Fields(s)
			if len(f) == 0 {
				// Empty is allowed and MEANINGFUL: it turns weeding off. Made explicit rather
				// than nil, so withDefaults does not read it as "absent" and fill it back in.
				v.cfg.KeepHeaders = []string{}
				return nil
			}
			for _, h := range f {
				if !strings.HasSuffix(h, ":") {
					return fmt.Errorf("%q needs a colon — NeoMutt matches header names as %q", h, h+":")
				}
			}
			v.cfg.KeepHeaders = f
			return nil
		},
		note: "the headers a message shows; everything else is hidden. Empty means hide nothing. " +
			"[h] in the index still toggles the full set on any one message.",
	},
	{
		label: "CA bundle",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.CertFile },
		set:   func(v *settingsView, s string) error { return setPath(&v.cfg.CertFile, s) },
		note: "what mbsync is told to trust. Wrong here fails at connection time with a message " +
			"about certificates that reads like the server's fault.",
	},
	{
		label: "real name",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.RealName },
		show:  func(v *settingsView) string { return orEmpty(v.cfg.RealName, "(not set)") },
		set: func(v *settingsView, s string) error {
			v.cfg.RealName = strings.TrimSpace(s)
			return nil
		},
		note: "offered as the display name when adding an account; each account may differ.",
	},
	{
		label: "editor",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.Editor },
		show:  func(v *settingsView) string { return orEmpty(v.cfg.Editor, "(NeoMutt's own default)") },
		set: func(v *settingsView, s string) error {
			v.cfg.Editor = strings.TrimSpace(s)
			return nil
		},
		note: "what NeoMutt opens to write a message. A path, not a variable: $EDITOR is read " +
			"once from whatever environment started NeoMutt, and composing from a launcher " +
			"that does not export it ends in an error naming the variable. Empty leaves it to " +
			"NeoMutt, which falls back to $VISUAL, $EDITOR, then vi.",
	},
	{
		label: "sidebar counts",
		kind:  skBool,
		get:   func(v *settingsView) string { return yesno(v.cfg.SidebarCounts) },
		set: func(v *settingsView, _ string) error {
			v.cfg.SidebarCounts = !v.cfg.SidebarCounts
			return nil
		},
		note: "the new/total numbers beside every mailbox. NeoMutt computes them only when this " +
			"is on, and its own default is off — which shows counts for the open mailbox and " +
			"nothing for the rest, and reads as a broken sidebar.",
	},
	{
		label: "wake every",
		kind:  skInt,
		get:   func(v *settingsView) string { return render.HumanEvery(v.cfg.WakeEvery) },
		set: func(v *settingsView, s string) error {
			n, err := render.ParseEvery(s)
			if err != nil || n < 1 {
				return fmt.Errorf("a time like 5s")
			}
			v.cfg.WakeEvery = n
			return nil
		},
		note: "how long NeoMutt waits for a keypress before doing any of the work below. The " +
			"outermost of the three: nothing under it can happen more often than this.",
	},
	{
		label: "check every",
		kind:  skInt,
		get:   func(v *settingsView) string { return render.HumanEvery(v.cfg.CheckEvery) },
		show:  func(v *settingsView) string { return effective(v.cfg.CheckEvery, v.cfg.WakeEvery, "wake") },
		set: func(v *settingsView, s string) error {
			n, err := render.ParseEvery(s)
			if err != nil || n < 1 {
				return fmt.Errorf("a time like 10s or 1m")
			}
			v.cfg.CheckEvery = n
			return nil
		},
		note: "how often NeoMutt looks for new mail on disk. Cheap — a stat per folder — and " +
			"separate from the recount below, which is not cheap on a large maildir.",
	},
	{
		label: "counts every",
		kind:  skInt,
		get:   func(v *settingsView) string { return render.HumanEvery(v.cfg.SidebarCountsEvery) },
		show: func(v *settingsView) string {
			return effective(v.cfg.SidebarCountsEvery,
				maxi(v.cfg.WakeEvery, v.cfg.CheckEvery), "wake/check")
		},
		set: func(v *settingsView, s string) error {
			n, err := render.ParseEvery(s)
			if err != nil || n < 1 {
				return fmt.Errorf("a time like 5s or 1m")
			}
			v.cfg.SidebarCountsEvery = n
			return nil
		},
		note: "how often the new/total numbers beside each mailbox are recomputed. The counts " +
			"are computed inside the check above, so that one is a ceiling over this.",
	},
	{
		label: "sync: create",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.Sync.Create },
		set:   func(v *settingsView, s string) error { return setSide(&v.cfg.Sync.Create, s) },
		note:  "which side may create a mailbox the other has. Both / Near / Far / None.",
	},
	{
		label: "sync: expunge",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.Sync.Expunge },
		set:   func(v *settingsView, s string) error { return setSide(&v.cfg.Sync.Expunge, s) },
		note:  "which side really deletes what is marked deleted. Both / Near / Far / None.",
	},
	{
		label: "sync: max messages",
		kind:  skInt,
		get:   func(v *settingsView) string { return strconv.Itoa(v.cfg.Sync.MaxMessages) },
		show: func(v *settingsView) string {
			if v.cfg.Sync.MaxMessages <= 0 {
				return "all"
			}
			return strconv.Itoa(v.cfg.Sync.MaxMessages)
		},
		set: func(v *settingsView, s string) error {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < 0 {
				return fmt.Errorf("a count, or 0 for all")
			}
			v.cfg.Sync.MaxMessages = n
			return nil
		},
		note: "how many messages a mailbox keeps locally. 0 is all of them.",
	},
	{
		label: "sync: expire unread",
		kind:  skBool,
		get:   func(v *settingsView) string { return yesno(v.cfg.Sync.ExpireUnread) },
		set: func(v *settingsView, _ string) error {
			v.cfg.Sync.ExpireUnread = !v.cfg.Sync.ExpireUnread
			return nil
		},
		note: "on: the cap above may delete mail you never read. That is how a cap loses mail.",
	},
	{
		label: "verify timeout",
		kind:  skInt,
		get:   func(v *settingsView) string { return strconv.Itoa(v.cfg.VerifyTimeoutSec) },
		show:  func(v *settingsView) string { return fmt.Sprintf("%d s", v.cfg.VerifyTimeoutSec) },
		set: func(v *settingsView, s string) error {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n <= 0 {
				return fmt.Errorf("seconds, greater than zero")
			}
			v.cfg.VerifyTimeoutSec = n
			return nil
		},
		note: "per stage of the live check. A dead host has to fail in seconds, not hang the screen.",
	},
	{
		label: "ask Mozilla's ISPDB",
		kind:  skBool,
		get:   func(v *settingsView) string { return yesno(v.cfg.ISPDB) },
		set: func(v *settingsView, _ string) error {
			v.cfg.ISPDB = !v.cfg.ISPDB
			return nil
		},
		note: "a privacy choice, not a technical one: the query tells Mozilla which mail domain " +
			"this machine is being set up for. Off leaves the built-in table, DNS and a direct probe.",
	},
	{
		label: "theme",
		kind:  skText,
		get:   func(v *settingsView) string { return v.cfg.Theme },
		show:  func(v *settingsView) string { return orEmpty(v.cfg.Theme, "(follows lvim-colorscheme)") },
		set: func(v *settingsView, s string) error {
			v.cfg.Theme = strings.TrimSpace(s)
			return nil
		},
		note: "the palette the interface paints with: a theme name, or a path to a palette file. " +
			"Left empty, mailforge follows whatever lvim-colorscheme says is active and falls back " +
			"to the built-in Everforest. [t] repaints now, [w] writes it.",
	},
}

func newSettingsView(c config.Config) *settingsView {
	return &settingsView{cfg: c, saved: c, buf: newInput("value", "")}
}

func (v *settingsView) Title() string { return "Settings" }

func (v *settingsView) capturesInput() bool { return v.editing }

func (v *settingsView) Init() tea.Cmd { return nil }

func (v *settingsView) dirty() bool { return !config.Same(v.cfg, v.saved) }

func (v *settingsView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case reloadMsg:
		// A hand edit to config.json is picked up here rather than only at startup — this
		// file is documented as editable by hand, and a screen that shows something else is
		// a screen that will be believed over the file.
		if v.dirty() {
			return v, nil
		}
		c, err := config.Load()
		if err != nil {
			return v, failure("%v", err)
		}
		v.cfg, v.saved = c, c
		return v, nil

	case tea.KeyMsg:
		if v.editing {
			switch msg.String() {
			case "esc":
				v.editing = false
				return v, nil
			case "enter":
				if err := settings[v.sel].set(v, v.buf.String()); err != nil {
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
			if v.sel < len(settings)-1 {
				v.sel++
			}
		case "k", "up":
			if v.sel > 0 {
				v.sel--
			}
		case "enter", " ":
			s := settings[v.sel]
			if s.kind == skBool {
				_ = s.set(v, "")
				return v, nil
			}
			v.editing = true
			v.buf.seed(s.get(v))
		case "w":
			return v, v.save()
		case "t":
			// Repaint from the theme named here without restarting. A theme is edited in another
			// window and looked at in this one, and "restart to see it" is what makes a colour
			// scheme take an evening. The styles are rebuilt in place, so the next draw is already
			// the new palette. This does not save — [w] does that.
			r := ApplyTheme(v.cfg)
			return v, status("painted with %s — %s", r.Name, r.Source)
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

// save writes the config AND regenerates what NeoMutt reads from it.
//
// The same rule the Keys tab follows: a settings file that has changed while the file NeoMutt
// reads has not is a half-applied state the user cannot see. Only the options are re-rendered —
// nothing here belongs to an account, so nothing here needs an account rewritten. A path that
// moved is a different matter and the doctor reports it; this is not the screen that moves mail.
func (v *settingsView) save() tea.Cmd {
	if err := config.Save(v.cfg); err != nil {
		return failure("%v", err)
	}
	v.saved = v.cfg

	// The theme is the one setting here that takes effect where it was changed rather than next
	// time, because it can: nothing but the interface's own styles depends on it.
	ApplyTheme(v.cfg)

	var p setup.Plan
	setup.ApplyOptions(v.cfg, &p)
	for _, s := range p.Steps {
		if s.Err != nil {
			return failure("%v", s.Err)
		}
	}
	return status("written to %s", config.Path())
}

func (v *settingsView) Render(w, h int) string {
	var b strings.Builder
	b.WriteString("  " + stDim.Render("file: ") + stFg.Render(config.Path()) + "\n")

	// Which palette is on screen and where it came from. A theme that quietly failed to resolve
	// looks exactly like one that was never set, and this is the only line that can tell them
	// apart — the interface itself would just be the built-in colours either way.
	t := CurrentTheme()
	b.WriteString("  " + stDim.Render("painted with: ") + stFg.Render(t.Name) +
		stDim.Render("  ·  "+t.Source) + "\n\n")

	for i, s := range settings {
		mark := "  "
		row := stRow
		if i == v.sel {
			mark = stKey.Render(pointer) + " "
			row = stRowSel
		}
		if v.editing && i == v.sel {
			b.WriteString(mark + cell(s.label, 22, row) + " " + v.buf.render(true) + "\n")
			continue
		}
		bg := row.GetBackground()
		b.WriteString(mark + cell(s.label, 22, row) + " " +
			cell(display(v, s), maxi(w-28, 12), stFg.Background(bg)) + "\n")
	}

	if v.sel < len(settings) {
		b.WriteString("\n" + indent(stDim.Render(wrapText(settings[v.sel].note, maxi(w-6, 30))), 2) + "\n")
		// What the loaded theme could not supply, said only on the row it concerns: a partial
		// palette is completed from the built-in, and that is worth knowing while you are looking
		// at the theme and noise anywhere else.
		if settings[v.sel].label == "theme" {
			for _, n := range t.Notes {
				b.WriteString(indent(stWarn.Render(wrapText(n, maxi(w-6, 30))), 2) + "\n")
			}
		}
	}
	if v.dirty() {
		b.WriteString("\n  " + stWarn.Render("changed — [w] writes it, [r] drops it"))
	}
	return b.String()
}

func (v *settingsView) Footer() string {
	if v.editing {
		return joinHints(hint("enter", "accept"), hint("esc", "cancel"))
	}
	f := []string{hint("j/k", "move"), hint("enter", "change"), hint("t", "repaint")}
	if v.dirty() {
		f = append(f, hint("w", "write"), hint("r", "revert"))
	}
	return joinHints(f...)
}

// display is what the table shows: the value, unless the row said it reads better as something
// else. One function, so the difference between "what it is" and "how it looks" has exactly one
// place to live and cannot leak back into the editor.
func display(v *settingsView, s setting) string {
	if s.show != nil {
		return s.show(v)
	}
	return s.get(v)
}

// setPath is the shared setter for the four fields that are paths: it refuses an empty one
// outright, because a path field left blank does not mean "default" here — it means the renderer
// writes to a filename of "" and the failure surfaces three screens later.
func setPath(dst *string, s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("a path is required")
	}
	*dst = s
	return nil
}

// setSide accepts only what mbsync accepts. Anything else is caught here rather than by mbsync
// hours later, when the message is about a config file mailforge wrote.
func setSide(dst *string, s string) error {
	switch v := strings.TrimSpace(s); strings.ToLower(v) {
	case "both":
		*dst = "Both"
	case "near":
		*dst = "Near"
	case "far":
		*dst = "Far"
	case "none":
		*dst = "None"
	default:
		return fmt.Errorf("Both, Near, Far or None")
	}
	return nil
}

// yesno is the PLAIN word. A styled one cannot go through `cell`: the escape codes are counted as
// letters, so a two-character value is truncated to "ye…" in a column twenty wide.
// effective shows what a nested timer actually comes to.
//
// NeoMutt's three intervals are ceilings over one another, and a setting under its ceiling is not
// wrong — it is simply not what happens. Saying so here is what keeps all three adjustable without
// turning the screen into a puzzle: the number stays the user's, and the consequence is visible
// beside it rather than discovered by watching a count fail to move.
func effective(want, ceiling int, by string) string {
	if want >= ceiling {
		return render.HumanEvery(want)
	}
	return render.HumanEvery(want) + "  → " + render.HumanEvery(ceiling) + " (" + by + ")"
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orEmpty(s, whenEmpty string) string {
	if strings.TrimSpace(s) == "" {
		return whenEmpty
	}
	return s
}

// maxi is the counterpart of mini in theme.go.
func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// wrapText folds a note to the width, breaking inside a word only when the word alone is wider
// than the line — a path is one word by this reckoning, and an unfolded path is a line that runs
// off the screen and takes the layout with it.
func wrapText(s string, width int) string {
	if width < 1 {
		width = 1
	}
	var out []string
	cur := ""
	flush := func() {
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	for _, word := range strings.Fields(s) {
		if len([]rune(word)) > width {
			flush()
			for r := []rune(word); len(r) > 0; {
				n := mini(width, len(r))
				out = append(out, string(r[:n]))
				r = r[n:]
			}
			continue
		}
		if cur != "" && len([]rune(cur))+1+len([]rune(word)) > width {
			flush()
		}
		if cur != "" {
			cur += " "
		}
		cur += word
	}
	flush()
	return strings.Join(out, "\n")
}
