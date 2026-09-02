package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// The fetch timer: a systemd USER unit, named after this program.
//
// `mailforge-mbsync`, not `mbsync`, because the obvious name is the one somebody's own hand-written
// timer already has. A generated file that lands on it would replace a working setup with this
// program's opinion of one, and the marker trick used for foreign config files does not exist for
// unit files — systemd owns the whole file or nothing.
const (
	ServiceUnit = "mailforge-mbsync.service"
	TimerUnit   = "mailforge-mbsync.timer"
)

// SystemdUserDir is where user units live, honouring XDG_CONFIG_HOME like systemd itself.
func SystemdUserDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

// MbsyncService renders the unit that does the fetching.
//
// The binary is named by ABSOLUTE PATH: a user unit starts with a PATH systemd assembled, not the
// one a login shell exports, and "mbsync" resolving under a terminal says nothing about whether it
// resolves here. The same reasoning that took $EDITOR out of the NeoMutt config.
//
// And the path is QUOTED, because systemd splits ExecStart on whitespace: a binary living under a
// directory with a space in its name produced a unit that failed at start with an error naming
// neither the space nor the path, and a timer that then never fetched anything.
func MbsyncService(c config.Config) string {
	// mailforge's own path, resolved the same way anything else on this machine would resolve
	// it. The unit runs `mailforge fetch`, not mbsync, so the fetch and the notification that
	// follows it stay one thing — see setup.Fetch.
	bin := sys.Which("mailforge")
	if bin == "" {
		if self, err := os.Executable(); err == nil {
			bin = self
		} else {
			bin = "mailforge"
		}
	}
	return HeaderUnit + `
[Unit]
Description=Fetch mail with mbsync (managed by mailforge)
Documentation=man:mbsync(1)
# Nothing to fetch without a network, and a failure on resume is a failure nobody reads.
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart="` + bin + `" fetch
# The password comes from pass, which needs the gpg agent to answer WITHOUT a prompt: there is
# no terminal here to type a passphrase into. An agent that has forgotten the key makes this
# unit fail rather than hang, which is the honest of the two.
Environment=GPG_TTY=
# A fetch that cannot finish in ten minutes is a fetch that is stuck.
TimeoutStartSec=600
Nice=10
IOSchedulingClass=idle
`
}

// MbsyncTimer renders the schedule.
func MbsyncTimer(c config.Config) string {
	// AccuracySec, and why it is not left alone: systemd's default is a whole MINUTE, which it
	// spends batching wakeups to save power. Under that default a thirty-second timer fires
	// roughly once a minute — the setting would be accepted, written, shown on screen, and
	// quietly not honoured. Tightened only when the interval is short enough to need it, so a
	// five-minute fetch still batches like everything else on the machine.
	//
	// The boundary INCLUDES one minute. A minute of slack on a one-minute timer is not slack,
	// it is a second interval of the same length: the fetch lands somewhere between one and two
	// minutes and never at the period that was asked for. The rule is that the tolerance has to
	// be small next to the interval, and at equal sizes it is not.
	accuracy := "1min"
	if c.SyncEvery <= 60 {
		accuracy = "1s"
	}

	return HeaderUnit + fmt.Sprintf(`
[Unit]
Description=Fetch mail every %s (managed by mailforge)

[Timer]
# The first run is soon after login rather than at once: logging in is when everything else on
# the machine is also starting, and mail is not the urgent one.
OnStartupSec=2min
# The same delay, but counted from when THIS TIMER started rather than from login.
#
# Without it the timer has no future to schedule whenever it is restarted mid-session — which is
# every time the interval is changed. OnStartupSec is measured from the user manager's startup and
# is spent within two minutes of logging in; OnUnitActiveSec is measured from the last activation
# of the service, which after a restart has not happened yet. Two bases, one long expired and one
# not yet existing, leave NextElapse at infinity: the unit reports "active", reports "success", and
# never fires again. That is how this machine went twenty days without an automatic fetch.
OnActiveSec=2min
OnUnitActiveSec=%ds
AccuracySec=%s
# Catch up after the machine was asleep, instead of waiting out the full interval again.
Persistent=true
Unit=%s

[Install]
WantedBy=timers.target
`, HumanEvery(c.SyncEvery), c.SyncEvery, accuracy, ServiceUnit)
}

// HumanEvery renders an interval the way it reads best: seconds under a minute, minutes when it
// divides evenly, hours likewise. One stored number, several ways to say it.
func HumanEvery(sec int) string {
	switch {
	case sec <= 0:
		return "never"
	case sec%3600 == 0 && sec >= 3600:
		return fmt.Sprintf("%d h", sec/3600)
	case sec%60 == 0 && sec >= 60:
		return fmt.Sprintf("%d min", sec/60)
	default:
		return fmt.Sprintf("%d s", sec)
	}
}

// ParseEvery reads an interval the way a person writes one: "30s", "5m", "2h", or a bare number
// of seconds. Returns seconds.
func ParseEvery(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, fmt.Errorf("a time like 30s, 5m or 2h, or 0 to turn the timer off")
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "h"):
		mult, s = 3600, strings.TrimSuffix(s, "h")
	case strings.HasSuffix(s, "min"):
		mult, s = 60, strings.TrimSuffix(s, "min")
	case strings.HasSuffix(s, "m"):
		mult, s = 60, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "s"):
		mult, s = 1, strings.TrimSuffix(s, "s")
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("a time like 30s, 5m or 2h, or 0 to turn the timer off")
	}
	return n * mult, nil
}

// HeaderUnit is the comment every generated unit carries.
var HeaderUnit = "# Generated by mailforge — do not edit by hand.\n" +
	"# This file is rewritten from ~/.config/mailforge/config.json on every apply.\n" +
	"# Change the interval in mailforge, in the Settings tab."
