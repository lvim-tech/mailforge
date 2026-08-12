package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestColorsSourcesTheLiveTheme is the frozen-theme defect, written down.
//
// themer rewrites ~/.config/neomutt/theme.conf on every theme switch. colors.rc used to be a COPY
// of it, taken once at first run — so the desktop changed colour and the mail client kept the ones
// it was born with. Nothing reported it: both files existed, both parsed, and the stale one was
// the one neomuttrc sourced.
func TestColorsSourcesTheLiveTheme(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "theme.conf")
	if err := os.WriteFile(live, []byte("color indicator default #272d2d\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := carryColors(LiveTheme(dir), "")

	if !strings.Contains(got, "source "+live+"\n") {
		t.Errorf("the live theme is not sourced:\n%s", got)
	}
	// The colour itself must NOT be in the file: if it is, this is a copy again and the next
	// theme switch will not reach NeoMutt.
	if strings.Contains(got, "#272d2d") {
		t.Error("the theme was copied rather than sourced — it will freeze at these colours")
	}
}

// TestColorsCopiesADeadBackup: with no live file there is nothing to follow, and the bytes of the
// old configuration are all that is left. Copying stays right there.
func TestColorsCopiesADeadBackup(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, "old-colors")
	if err := os.WriteFile(backup, []byte("color normal default default\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := carryColors(LiveTheme(dir), backup) // LiveTheme finds nothing: no theme.conf here

	if !strings.Contains(got, "color normal default default") {
		t.Errorf("the backup was not carried over:\n%s", got)
	}
	if strings.Contains(got, "source ") {
		t.Error("a source line was written with no live theme to point at")
	}
}

// TestLiveThemeIgnoresADirectory: `source` on a directory is an error banner at every start.
func TestLiveThemeIgnoresADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "theme.conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := LiveTheme(dir); got != "" {
		t.Errorf("a directory was offered as a theme file: %q", got)
	}
}

// TestNoThemeAndNoBackupSaysSo: silence and "there was nothing to carry" look identical on disk.
func TestNoThemeAndNoBackupSaysSo(t *testing.T) {
	got := carryColors(LiveTheme(t.TempDir()), "")
	if strings.Contains(got, "source ") {
		t.Error("a source line was written with nothing to point at")
	}
	if !strings.Contains(got, "nothing was carried over") {
		t.Errorf("the empty case is not explained:\n%s", got)
	}
}
