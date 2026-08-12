package setup

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/lvim-tech/mailforge/internal/config"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// Fetch runs mbsync for every account and then tells whoever is watching.
//
// ONE command for both callers — the systemd timer and the key in NeoMutt — because the two used
// to be one shell line each, and the second thing they had to do was added to only one of them.
// A status bar that updates after the timer but not after the key is a bar the user learns not to
// trust, and the fix belongs where the fetch is, not in each place that starts one.
func Fetch(_ config.Config) int {
	bin := sys.Which("mbsync")
	if bin == "" {
		fmt.Fprintln(os.Stderr, "mailforge: mbsync is not on PATH")
		return 1
	}

	cmd := exec.Command(bin, "-a")
	// Straight through, not captured: under the timer this is the journal, and at a shell it is
	// the terminal. A fetch that failed should say why in the place its output already goes.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Run()

	if err != nil {
		var ee *exec.ExitError
		if ok := asExit(err, &ee); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "mailforge:", err)
		return 1
	}
	return 0
}

func asExit(err error, out **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*out = ee
	}
	return ok
}
