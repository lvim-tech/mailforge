package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// input is a one-line text field.
//
// Hand-rolled rather than pulled from a widget library for one reason that matters in this program:
// mailforge must never end up with a secret in a component whose lifetime it does not control. This
// field is used only for names, comments and hostnames — never for a passphrase, which goes
// straight into ssh-keygen's own prompt — and keeping it small makes that boundary easy to see.
type input struct {
	label  string
	value  []rune
	cursor int
	width  int
	hint   string

	// masked hides the contents behind dots.
	//
	// Used for the parts of the printing rule. They are not passwords, but they are the
	// legend of the paper: somebody who reads "q7" off this screen can turn a printed sheet
	// back into the password on it. A field that shows them while they are being chosen
	// undoes the point of storing them encrypted afterwards.
	masked bool

	// fresh marks a field that was seeded with the value it is editing and has not been typed
	// into yet. The first printable key REPLACES that value, the way a field whose text is
	// selected on focus behaves everywhere else.
	//
	// Without it, seeding is a trap on short values: opening a timeout of 10 and typing 30
	// gives 1030, and it saves. Seeding is still right — a path is long and usually needs a
	// small correction, not retyping — so the answer is to make the seeded text behave like a
	// selection rather than to stop seeding.
	fresh bool
}

func newInput(label, hint string) input {
	return input{label: label, hint: hint, width: 40}
}

func newMaskedInput(label, hint string) input {
	return input{label: label, hint: hint, width: 40, masked: true}
}

func (i *input) set(s string) {
	i.value = []rune(s)
	i.cursor = len(i.value)
	i.fresh = false
}

// seed fills the field with the value being edited and marks it replaceable — see `fresh`.
func (i *input) seed(s string) {
	i.set(s)
	i.fresh = len(i.value) > 0
}

func (i input) String() string { return string(i.value) }

func (i input) empty() bool { return len(strings.TrimSpace(string(i.value))) == 0 }

// update handles a key press, returning whether it was consumed.
func (i *input) update(msg tea.KeyMsg) bool {
	// A seeded value is replaced by the first thing typed over it, and kept the moment the
	// user does anything that says they mean to edit rather than replace: moving, deleting,
	// or landing anywhere but the end.
	if i.fresh {
		switch msg.Type {
		case tea.KeyRunes, tea.KeySpace:
			i.value, i.cursor = nil, 0
		}
		i.fresh = false
	}
	switch msg.Type {
	case tea.KeyRunes:
		i.value = append(i.value[:i.cursor], append([]rune(string(msg.Runes)), i.value[i.cursor:]...)...)
		i.cursor += len(msg.Runes)
		return true
	case tea.KeySpace:
		i.value = append(i.value[:i.cursor], append([]rune{' '}, i.value[i.cursor:]...)...)
		i.cursor++
		return true
	case tea.KeyBackspace:
		if i.cursor > 0 {
			i.value = append(i.value[:i.cursor-1], i.value[i.cursor:]...)
			i.cursor--
		}
		return true
	case tea.KeyDelete:
		if i.cursor < len(i.value) {
			i.value = append(i.value[:i.cursor], i.value[i.cursor+1:]...)
		}
		return true
	case tea.KeyLeft:
		if i.cursor > 0 {
			i.cursor--
		}
		return true
	case tea.KeyRight:
		if i.cursor < len(i.value) {
			i.cursor++
		}
		return true
	case tea.KeyHome, tea.KeyCtrlA:
		i.cursor = 0
		return true
	case tea.KeyEnd, tea.KeyCtrlE:
		i.cursor = len(i.value)
		return true
	case tea.KeyCtrlU:
		i.value, i.cursor = nil, 0
		return true
	}
	return false
}

// render draws the field, marking the cursor position and showing the hint when empty.
func (i input) render(focused bool) string {
	if i.masked {
		label := stDim.Render(i.label + ": ")
		if focused {
			label = stKey.Render(i.label + ": ")
		}
		if len(i.value) == 0 {
			if focused {
				return label + stRowSel.Render(" ")
			}
			return label + stDim.Render(i.hint)
		}
		// One dot per CHARACTER and the count beside it — the same honesty the passphrase
		// field owes: a mask that counts bytes says nothing true about what was typed.
		dots := strings.Repeat("•", len(i.value))
		if focused {
			dots += stRowSel.Render(" ")
		}
		return label + stFg.Render(dots) + stDim.Render(fmt.Sprintf("  (%d)", len(i.value)))
	}
	text := string(i.value)
	if len(i.value) == 0 {
		// The hint STAYS while the field is focused, with the cursor in front of it.
		//
		// It used to be dropped on focus, and that read as deletion: the hints name plausible
		// values — "993", "smtp.example.com" — so an empty field looks filled in, and moving
		// onto it made the apparent value vanish under the cursor. Nothing was ever stored, but
		// the screen said otherwise both before and after.
		if focused {
			return i.labelOf(focused) + stRowSel.Render(" ") + stDim.Render(i.hint)
		}
		return i.labelOf(focused) + stDim.Render(i.hint)
	}
	if focused && i.fresh {
		// Seeded and not yet typed into: the next printable key replaces the whole value, so it
		// is drawn the way selected text is drawn everywhere else. Without that, the rule is
		// invisible — the field looks like an ordinary one whose cursor sits at the end, and the
		// only ways to discover what it does are to lose a value or to read the source.
		return i.labelOf(focused) + stRowSel.Render(string(i.value))
	}
	if focused {
		before, after := string(i.value[:i.cursor]), string(i.value[i.cursor:])
		cur := " "
		if i.cursor < len(i.value) {
			cur = string(i.value[i.cursor])
			after = string(i.value[i.cursor+1:])
		}
		text = before + stRowSel.Render(cur) + after
	}
	return i.labelOf(focused) + text
}

func (i input) labelOf(focused bool) string {
	if focused {
		return stKey.Render(i.label + ": ")
	}
	return stDim.Render(i.label + ": ")
}
