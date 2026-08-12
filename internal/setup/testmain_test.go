package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lvim-tech/mailforge/internal/sys"
)

// TestMain points XDG_CONFIG_HOME at a scratch directory for the WHOLE package.
//
// Not defence in depth — the fix for something that happened. The registry's location comes from
// config.Path(), not from the Config a test hands to a function, so a test that redirected every
// field of its fixture still saved that fixture's two-line registry over the real one on the
// machine running the tests. It cost the user four configured accounts.
//
// Per package rather than per test, because the next test to be written is the one that would
// forget, and by then the failure is somebody's mail setup rather than a red bar.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mailforge-setup-test-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config")); err != nil {
		panic(err)
	}
	// And the service manager is out of reach too. ApplyTimer enables a unit; a test doing that
	// on the machine running the tests is the filesystem mistake above, one layer out.
	systemctl = func(args ...string) (sys.Result, error) {
		return sys.Result{Stdout: "active"}, nil
	}

	loginctl = func(args ...string) (sys.Result, error) {
		return sys.Result{Stdout: "no"}, nil
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
