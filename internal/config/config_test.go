package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbsentFileIsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatalf("a missing config is not an error: %v", err)
	}
	if !Same(c, Default()) {
		t.Errorf("a machine with no config should behave exactly like Default()")
	}
	if Exists() {
		t.Errorf("Exists reported a file that is not there")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	want := Default()
	want.RealName = "Biser Stoilov"
	want.VerifyTimeoutSec = 30
	want.ISPDB = false
	want.Sync.MaxMessages = 500

	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !Same(got, want) {
		t.Errorf("what came back is not what went in:\n got %+v\nwant %+v", got, want)
	}
}

// TestHandEditedFileIsCompleted: config.json is documented as editable by hand, so a file with
// three fields in it has to produce a whole configuration. Without this the program writes to a
// path of "" and the failure surfaces somewhere else entirely.
func TestHandEditedFileIsCompleted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "mailforge"), 0o700); err != nil {
		t.Fatal(err)
	}
	partial := []byte(`{"real_name":"Someone","verify_timeout_sec":0}`)
	if err := os.WriteFile(filepath.Join(dir, "mailforge", "config.json"), partial, 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.RealName != "Someone" {
		t.Errorf("the field that WAS set got lost: %q", c.RealName)
	}
	if c.MaildirRoot != d.MaildirRoot || c.PassPrefix != d.PassPrefix || c.Sync.Create != d.Sync.Create {
		t.Errorf("absent fields were not filled from Default: %+v", c)
	}
	if c.VerifyTimeoutSec != d.VerifyTimeoutSec {
		t.Errorf("a nonsensical zero timeout was kept: %d", c.VerifyTimeoutSec)
	}
}

// TestBrokenFileIsReported: a config that cannot be parsed must not be silently replaced by
// defaults. Settings that quietly reset are how a machine ends up rendering to paths the user
// stopped using, and "it worked yesterday" starts there.
func TestBrokenFileIsReported(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "mailforge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mailforge", "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("a broken config was accepted in silence")
	}
}

// TestWriteAtomicLeavesNoDebris: the temp file must not survive a successful write, and the
// result must carry the mode asked for — this is the writer every other file in the program uses.
func TestWriteAtomicLeavesNoDebris(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "thing.json")
	if err := WriteAtomic(p, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "two\n" {
		t.Errorf("the second write did not replace the first: %q", b)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode is %v, want 0600", fi.Mode().Perm())
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		names := []string{}
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Errorf("temp files left behind: %v", names)
	}
}
