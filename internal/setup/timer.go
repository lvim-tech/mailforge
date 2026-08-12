package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/render"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// systemctl is the one call in this package that reaches OUT of the filesystem and touches the
// machine's service manager. It is a variable so the tests can take that away: a test that
// redirects every path it writes to still enables a unit in the real session otherwise, which is
// the same mistake as writing the real registry from a fixture.
var systemctl = func(args ...string) (sys.Result, error) {
	return sys.Run("systemctl", append([]string{"--user"}, args...)...)
}

// loginctl is the same seam for the linger flag. See systemctl above.
var loginctl = func(args ...string) (sys.Result, error) {
	return sys.Run("loginctl", args...)
}

// Lingering reports whether systemd keeps this user's services alive after logout.
func Lingering() bool {
	res, err := loginctl("show-user", os.Getenv("USER"), "--property=Linger", "--value")
	if err != nil {
		return false
	}
	return strings.TrimSpace(res.Stdout) == "yes"
}

// ApplyTimer puts the systemd user timer in step with the config.
//
// It writes files AND runs systemctl, which is more than the rest of this program does — every
// other artefact is inert until something else reads it. A unit file is not: systemd has already
// read the old one, and a new one on disk that nobody reloaded is a timer running yesterday's
// schedule while the settings screen shows today's. The reload is part of writing it, not a
// courtesy afterwards.
//
// Removing is the mirror: the timer is stopped and disabled BEFORE the files go, because systemd
// cannot stop a unit whose file it can no longer read.
func ApplyTimer(c config.Config, p *Plan) {
	dir := render.SystemdUserDir()
	svc := filepath.Join(dir, render.ServiceUnit)
	tmr := filepath.Join(dir, render.TimerUnit)

	if !sys.Have("systemctl") {
		Write(p, "systemd timer", func() (string, error) {
			return "no systemctl on this machine — nothing to schedule with", nil
		})
		return
	}

	if c.SyncEvery <= 0 {
		Write(p, "stop "+render.TimerUnit, func() (string, error) {
			if _, err := os.Stat(tmr); os.IsNotExist(err) {
				return "was not there", nil
			}
			// Errors are NOT fatal here: a unit that was never enabled reports a failure for
			// being asked to stop, and that is the state being asked for.
			_, _ = systemctl("disable", "--now", render.TimerUnit)
			for _, f := range []string{tmr, svc} {
				if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
					return "", err
				}
			}
			_, _ = systemctl("daemon-reload")
			return "removed", nil
		})
		return
	}

	Write(p, "write "+tmr, func() (string, error) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		changed := false
		for f, body := range map[string]string{
			svc: render.MbsyncService(c),
			tmr: render.MbsyncTimer(c),
		} {
			if same, err := unchanged(f, body); err == nil && same {
				continue
			}
			if err := config.WriteAtomic(f, []byte(body), 0o644); err != nil {
				return "", err
			}
			changed = true
		}
		if !changed {
			return "already up to date", nil
		}
		return "written", nil
	})

	// Lingering: whether the timer outlives the login session. Set BEFORE the timer is
	// enabled, so a first run cannot happen under the wrong assumption about how long it lives.
	if sys.Have("loginctl") {
		Write(p, "keep fetching after logout: "+yesNo(c.FetchWhenLoggedOut), func() (string, error) {
			if Lingering() == c.FetchWhenLoggedOut {
				return "already so", nil
			}
			verb := "disable-linger"
			if c.FetchWhenLoggedOut {
				verb = "enable-linger"
			}
			res, err := loginctl(verb, os.Getenv("USER"))
			if err != nil || !res.OK() {
				// NOT fatal: on a machine whose policy reserves this for root, the timer
				// still works for as long as somebody is logged in, and saying so is more
				// use than refusing to schedule anything at all.
				return "refused by the system — the timer will only run while you are logged in", nil
			}
			return verb, nil
		})
	}

	Write(p, "enable it, every "+render.HumanEvery(c.SyncEvery), func() (string, error) {
		if res, err := systemctl("daemon-reload"); err != nil || !res.OK() {
			return "", fmt.Errorf("daemon-reload: %s", res.Stderr)
		}
		res, err := systemctl("enable", "--now", render.TimerUnit)
		if err != nil || !res.OK() {
			return "", fmt.Errorf("%s", res.Stderr)
		}
		return "active", nil
	})
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
