package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/setup"
)

// doctorView reports whether this machine can host the setup, and later whether the setup it
// hosts still agrees with itself.
//
// The checks run OFF the draw path, as a command: they spawn four processes and read the store,
// and doing that inside Render would tie the frame rate to how fast gpg starts.
type doctorView struct {
	cfg    config.Config
	rep    setup.Report
	sel    int
	loaded bool
	plan   setup.Plan
}

func newDoctorView(c config.Config) *doctorView { return &doctorView{cfg: c} }

func (v *doctorView) Title() string { return "Doctor" }

func (v *doctorView) Init() tea.Cmd { return v.run() }

type doctorDone setup.Report

// run does both halves: whether this machine CAN host the setup, and whether the setup it hosts
// still agrees with itself. They are separate questions and a missing `pass` should not be
// reported as an account problem, so Environment answers the first and Audit the second — but the
// reader wants one screen, so they are joined here rather than in two tabs.
func (v *doctorView) run() tea.Cmd {
	c := v.cfg
	return func() tea.Msg {
		rep := setup.Environment(c)
		reg, err := account.Load()
		if err != nil {
			rep.Findings = append(rep.Findings, setup.Finding{
				Level: setup.Fail, Subject: "registry", Message: err.Error(),
				Action: "fix " + account.RegistryPath() + " by hand",
			})
			return doctorDone(rep)
		}
		rep.Findings = append(rep.Findings, setup.Audit(c, reg).Findings...)
		return doctorDone(rep)
	}
}

// fix re-applies everything that is a pure re-render — every drift class the audit can repair
// without asking a person. A missing password or an unfetched maildir is deliberately NOT in
// reach: a button claiming to fix everything would have to lie about those two.
func (v *doctorView) fix() tea.Cmd {
	c := v.cfg
	return func() tea.Msg {
		reg, err := account.Load()
		if err != nil {
			return statusMsg{text: err.Error(), err: true}
		}
		return fixed(setup.Fix(c, &reg))
	}
}

type fixed setup.Plan

func (v *doctorView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case doctorDone:
		v.rep, v.loaded = setup.Report(msg), true
		if v.sel >= len(v.rep.Findings) {
			v.sel = 0
		}
		return v, nil

	case fixed:
		v.plan = setup.Plan(msg)
		return v, v.run()

	case reloadMsg:
		return v, v.run()

	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if v.sel < len(v.rep.Findings)-1 {
				v.sel++
			}
		case "k", "up":
			if v.sel > 0 {
				v.sel--
			}
		case "g":
			v.sel = 0
		case "G":
			v.sel = maxi(len(v.rep.Findings)-1, 0)
		case "r":
			return v, v.run()
		case "f":
			return v, v.fix()
		}
	}
	return v, nil
}

func (v *doctorView) Render(w, h int) string {
	if !v.loaded {
		return stDim.Render("  looking…")
	}
	var b strings.Builder

	fail, warn, ok := v.rep.Counts()
	b.WriteString("  ")
	b.WriteString(stBad.Render(fmt.Sprintf("fail %d", fail)) + "   ")
	b.WriteString(stWarn.Render(fmt.Sprintf("warn %d", warn)) + "   ")
	b.WriteString(stGood.Render(fmt.Sprintf("ok %d", ok)))
	b.WriteString("\n\n")

	// Worst first: a screen that opens on the things that are fine teaches the reader to
	// scroll past the part they came for.
	order := []setup.Level{setup.Fail, setup.Warn, setup.OK}
	start, end := window(len(v.rep.Findings), v.sel, maxi(h-6, 1))
	if start > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("     … %d above", start)) + "\n")
	}
	idx := 0
	for _, lvl := range order {
		for _, f := range v.rep.Findings {
			if f.Level != lvl {
				continue
			}
			if idx < start || idx >= end {
				idx++
				continue
			}
			mark := "  "
			row := stRow
			if idx == v.sel {
				mark = stKey.Render(pointer) + " "
				row = stRowSel
			}
			bg := row.GetBackground()
			dotStyle := stGood.Background(bg)
			switch lvl {
			case setup.Fail:
				dotStyle = stBad.Background(bg)
			case setup.Warn:
				dotStyle = stWarn.Background(bg)
			}
			msgW := maxi(w-30, 8)
			b.WriteString(mark + dotStyle.Render("●") + " " +
				cell(trunc(f.Subject, 18), 18, row) + " " +
				cell(f.Message, msgW, row) + "\n")

			if idx == v.sel {
				// The row under the cursor says all of itself. Truncation is fine for
				// reading down a column; it is not fine for the one line you stopped on,
				// where what got cut is usually the part that tells you what to do.
				if len([]rune(f.Message)) > msgW {
					b.WriteString(indent(stFg.Render(wrapText(f.Message, maxi(w-6, 24))), 5) + "\n")
				}
				if f.Action != "" {
					for _, l := range hanging("→ ", f.Action, maxi(w-6, 24)) {
						b.WriteString(indent(stNote.Render(l), 5) + "\n")
					}
				}
			}
			idx++
		}
	}
	if end < len(v.rep.Findings) {
		b.WriteString(stDim.Render(fmt.Sprintf("     … %d below", len(v.rep.Findings)-end)) + "\n")
	}
	return b.String()
}

func (v *doctorView) Footer() string {
	f := []string{hint("j/k", "move"), hint("g/G", "top/bottom"), hint("r", "check again")}
	if fail, warn, _ := v.rep.Counts(); fail+warn > 0 {
		f = append(f, hint("f", "re-render what can be fixed"))
	}
	return joinHints(f...)
}

// trunc cuts a string to n runes, marking that it was cut.
func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
