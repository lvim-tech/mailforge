package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lvim-tech/mailforge/internal/config"
)

// view is one tab. Each owns its state and its own key handling; the shell below only routes.
type view interface {
	Init() tea.Cmd
	Update(tea.Msg) (view, tea.Cmd)
	Render(width, height int) string
	Title() string
	Footer() string
}

// capturer is implemented by a view that is currently taking plain letters as text.
//
// Without it the shell would steal `q` and `a` from every form the moment somebody typed an
// address containing them. A view says so for itself because only it knows which of its modes is
// a form and which is a list.
type capturer interface{ capturesInput() bool }

// statusMsg carries a one-line result to the shell's status bar.
type statusMsg struct {
	text string
	err  bool
}

func status(format string, a ...any) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: fmt.Sprintf(format, a...)} }
}

func failure(format string, a ...any) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: fmt.Sprintf(format, a...), err: true} }
}

// reloadMsg asks a view to re-read the world after something changed it.
//
// Sent to the tab being opened, not broadcast: this program's tabs read files and spawn processes
// (`pass`, `mbsync --version`), and doing that for six tabs on every keypress would make tabbing
// through the interface a burst of work nobody asked for.
type reloadMsg struct{}

// Model is the application shell: a tab strip, the active view, and a status line.
type Model struct {
	views  []view
	active int
	w, h   int
	status string
	stErr  bool
	quit   bool
}

// New builds the application.
func New(c config.Config) Model {
	// Paint from the configured theme before the first frame. The built-in is already in force
	// from init(), so this only ever moves the colours towards what the user asked for — and if
	// nothing resolves, it stays exactly where it was.
	ApplyTheme(c)
	return Model{
		views: []view{
			newAccountsView(c),
			newDoctorView(c),
			newKeysView(c),
			newSettingsView(c),
		},
	}
}

func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, v := range m.views {
		if c := v.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case statusMsg:
		m.status, m.stErr = msg.text, msg.err
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "tab", "right":
			if !m.viewCaptures() {
				m.active = (m.active + 1) % len(m.views)
				m.status = ""
				return m, m.enterActive()
			}
		case "shift+tab", "left":
			if !m.viewCaptures() {
				m.active = (m.active - 1 + len(m.views)) % len(m.views)
				m.status = ""
				return m, m.enterActive()
			}
		case "q":
			if !m.viewCaptures() {
				m.quit = true
				return m, tea.Quit
			}
		}
	}

	// The RESULT of something the accounts tab started belongs to the accounts tab, whichever one
	// is on screen when it lands. The wizard pins the tab while it runs, but a re-verify or an
	// index re-render started from the list does not, so tabbing away while one was in flight
	// delivered its answer to the Doctor, which dropped it — leaving "checking …" over a list that
	// had long finished, and any error it reported never shown at all.
	if i, ok := m.owner(msg); ok {
		nv, cmd := m.views[i].Update(msg)
		m.views[i] = nv
		return m, cmd
	}

	before := screenOf(m.views[m.active])
	nv, cmd := m.views[m.active].Update(msg)
	m.views[m.active] = nv
	// A status line describes the action that produced it. Once the view has moved to another
	// screen it is describing something no longer on display — which is how "the SMTP host is
	// required" ended up sitting under the password step, an error about a field that step does
	// not have. Cleared HERE, synchronously, rather than by having every transition remember to
	// emit an empty status: there are sixteen of them, and the seventeenth would forget.
	if after := screenOf(nv); after != before {
		m.status, m.stErr = "", false
	}
	return m, cmd
}

// owner names the view a message belongs to when that is not simply the visible one.
//
// Only the outcomes of work the accounts tab starts are listed. Everything else — a key, a resize,
// a reload — is about whatever is on screen, and routing it anywhere else would be wrong.
func (m Model) owner(msg tea.Msg) (int, bool) {
	switch msg.(type) {
	case accountsLoaded, discovered, verified, reverified, applied, signatureEdited:
	default:
		return 0, false
	}
	for i, v := range m.views {
		if _, ok := v.(*accountsView); !ok {
			continue
		}
		if i == m.active {
			// Already the active view: let the ordinary path handle it, so a screen change
			// still clears a status line that has stopped being true.
			return 0, false
		}
		return i, true
	}
	return 0, false
}

// screener is implemented by a view whose screen can change without the tab changing — a wizard.
// Views without one are a single screen, and report the same id forever.
type screener interface{ screenID() int }

func screenOf(v view) int {
	if s, ok := v.(screener); ok {
		return s.screenID()
	}
	return 0
}

// enterActive hands the tab just opened a reloadMsg, so what it shows is what is on disk now
// rather than what was there when the program started.
func (m *Model) enterActive() tea.Cmd {
	nv, cmd := m.views[m.active].Update(reloadMsg{})
	m.views[m.active] = nv
	return cmd
}

// viewCaptures reports whether the active view is in a mode where plain letters are text input.
func (m Model) viewCaptures() bool {
	if c, ok := m.views[m.active].(capturer); ok {
		return c.capturesInput()
	}
	return false
}

func (m Model) View() string {
	if m.quit {
		return ""
	}
	if m.w == 0 {
		return "…"
	}

	head := stTitle.Render("mailforge") + " " + m.tabStrip(m.w-len("mailforge "))

	st := ""
	if m.status != "" {
		if m.stErr {
			st = stErr.Render("✕ " + m.status)
		} else {
			st = stOK.Render("✓ " + m.status)
		}
	}

	// The shell's own keys are offered only when they WORK. Inside a form, tab moves between
	// fields and q is a letter somebody is typing — advertising them there names two keys that
	// do something else, which is worse than naming none.
	var shell []string
	if !m.viewCaptures() {
		shell = []string{hint("tab", "next tab"), hint("q", "quit")}
	}
	foot := wrapHints(append(shell, splitHints(m.views[m.active].Footer())...), m.w-2)

	// The footer is measured before the body is sized, because how many rows it wants decides
	// what is left. The composition below costs 3 + bodyH + footLines rows: the status line
	// takes one even when it is empty.
	footLines := strings.Count(foot, "\n") + 1
	if room := m.h - 3; room < footLines {
		foot = clampLines(foot, room)
		footLines = strings.Count(foot, "\n") + 1
		if room <= 0 {
			foot, footLines = "", 0
		}
	}
	bodyH := m.h - 3 - footLines
	if bodyH < 0 {
		bodyH = 0
	}
	// Cut to the budget AND padded to it: a body shorter than its height used to pull the status
	// line and the key hints up under it, so they sat just below the content — near the top of
	// the window on a short list, somewhere else on a long one. The hints belong on the bottom
	// edge of the terminal, in the same place every time, and only the space between the tab
	// strip and them scrolls.
	body := padLines(clampLines(m.views[m.active].Render(m.w, bodyH), bodyH), bodyH)

	// The invariant, checked at the one place it can be. A frame one row taller than the
	// terminal makes the terminal scroll, and under the alternate screen the row lost off the
	// TOP is the tab strip — the one that says where you are.
	return clampLines(head+"\n\n"+body+"\n"+st+"\n"+foot, m.h)
}

// tabStrip draws the tabs, in the widest form that fits.
func (m Model) tabStrip(avail int) string {
	var b strings.Builder
	full := 0
	for _, v := range m.views {
		full += lipgloss.Width(stTab.Render(v.Title()))
	}
	if full <= avail {
		for i, v := range m.views {
			if i == m.active {
				b.WriteString(stTabOn.Render(v.Title()))
				continue
			}
			b.WriteString(stTab.Render(v.Title()))
		}
		return b.String()
	}
	// Too narrow for the padded strip: name only the tab you are on, and say where it sits.
	b.WriteString(stTabOn.Render(m.views[m.active].Title()))
	b.WriteString(stDim.Render(fmt.Sprintf("  %d/%d  tab moves", m.active+1, len(m.views))))
	return b.String()
}

// splitHints takes a rendered footer apart again at the double space that joins its pairs, so the
// shell's own hints and a view's can be folded together as one list.
func splitHints(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "  ")
}

// wrapHints folds the hint list to the width, and is the reason the footer may be several rows.
//
// Measured with lipgloss.Width, not len: every hint carries colour, and the escape codes are not
// characters on the screen however much they weigh in the string.
func wrapHints(parts []string, width int) string {
	if width < 10 {
		width = 10
	}
	var lines []string
	cur := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		next := p
		if cur != "" {
			next = cur + "  " + p
		}
		if lipgloss.Width(next) > width && cur != "" {
			lines = append(lines, cur)
			cur = p
			continue
		}
		cur = next
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	for i := range lines {
		lines[i] = stFooter.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}
