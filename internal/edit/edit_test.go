package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpFile(t *testing.T, content string) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "mbsyncrc")
	if content != "" {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpsertCreatesTheFile(t *testing.T) {
	p := tmpFile(t, "")
	r, err := Upsert(p, "a@x.com", "Host one\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed || !r.Created {
		t.Errorf("creating a file should report both: %+v", r)
	}
	got := readFile(t, p)
	if !strings.Contains(got, Open("a@x.com")) || !strings.Contains(got, Close("a@x.com")) {
		t.Errorf("fences missing:\n%s", got)
	}
	if !strings.Contains(got, "Host one") {
		t.Errorf("body missing:\n%s", got)
	}
}

// TestSecondApplyIsInert is the property that makes apply safe to run repeatedly: identical
// content must produce no write at all — no backup, no new mtime, nothing.
func TestSecondApplyIsInert(t *testing.T) {
	p := tmpFile(t, "")
	if _, err := Upsert(p, "a@x.com", "Host one\n", false); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Upsert(p, "a@x.com", "Host one\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Changed {
		t.Errorf("an identical apply reported a change")
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("an identical apply rewrote the file")
	}
	// And no backup was taken, because nothing was changed.
	if ents, err := os.ReadDir(BackupDir()); err == nil && len(ents) > 0 {
		t.Errorf("an inert apply left %d backups", len(ents))
	}
}

// TestEverythingOutsideTheFencesSurvives is the whole promise of the engine. The foreign content
// here deliberately includes something that LOOKS like a fence for another id.
func TestEverythingOutsideTheFencesSurvives(t *testing.T) {
	before := "# my own notes\nIMAPStore hand-written\nHost example.invalid\n\n"
	after := "\n# and more of mine\nChannel mine\n"
	orig := before + Open("a@x.com") + "\n" + notice + "\nold body\n" + Close("a@x.com") + "\n" + after
	p := tmpFile(t, orig)

	if _, err := Upsert(p, "a@x.com", "new body\n", false); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if !strings.HasPrefix(got, before) {
		t.Errorf("what came before the block was changed:\n%q", got)
	}
	if !strings.HasSuffix(got, after) {
		t.Errorf("what came after the block was changed:\n%q", got)
	}
	if strings.Contains(got, "old body") || !strings.Contains(got, "new body") {
		t.Errorf("the block was not replaced:\n%s", got)
	}
}

func TestRemoveTakesTheBlankLineItAdded(t *testing.T) {
	p := tmpFile(t, "Host mine\n")
	if _, err := Upsert(p, "a@x.com", "body\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(p, "a@x.com", false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "Host mine\n" {
		t.Errorf("add then remove did not return the file to what it was:\n%q", got)
	}
}

func TestRemoveOfAnAbsentBlockIsSuccess(t *testing.T) {
	p := tmpFile(t, "Host mine\n")
	r, err := Remove(p, "nobody@x.com", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Changed {
		t.Errorf("removing nothing reported a change")
	}
	if got := readFile(t, p); got != "Host mine\n" {
		t.Errorf("the file was touched: %q", got)
	}
}

// TestRefusals: a file whose fence structure is not exactly sane is a file this program does not
// understand, and it must stop rather than repair on a guess.
func TestRefusals(t *testing.T) {
	cases := map[string]string{
		"open with no close": Open("a@x.com") + "\nbody\n",
		"close with no open": "body\n" + Close("a@x.com") + "\n",
		"mismatched ids":     Open("a@x.com") + "\nbody\n" + Close("b@x.com") + "\n",
		"nested":             Open("a@x.com") + "\n" + Open("b@x.com") + "\nbody\n" + Close("b@x.com") + "\n" + Close("a@x.com") + "\n",
		"same id twice": Open("a@x.com") + "\none\n" + Close("a@x.com") + "\n" +
			Open("a@x.com") + "\ntwo\n" + Close("a@x.com") + "\n",
	}
	for name, content := range cases {
		p := tmpFile(t, content)
		if _, err := Upsert(p, "a@x.com", "new\n", false); err == nil {
			t.Errorf("%s: accepted a file it cannot understand", name)
		} else if !strings.Contains(err.Error(), "line ") {
			t.Errorf("%s: the refusal does not name a line: %v", name, err)
		}
		// And the file must be untouched after a refusal.
		if got := readFile(t, p); got != content {
			t.Errorf("%s: the file was modified despite the refusal", name)
		}
	}
}

// TestOtherIDsAreLeftAlone: two accounts share the file, and editing one must not disturb the
// other — including its fences.
func TestOtherIDsAreLeftAlone(t *testing.T) {
	p := tmpFile(t, "")
	if _, err := Upsert(p, "a@x.com", "A body\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Upsert(p, "b@x.com", "B body\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Upsert(p, "a@x.com", "A changed\n", false); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if !strings.Contains(got, "A changed") || !strings.Contains(got, "B body") {
		t.Errorf("editing one block disturbed the other:\n%s", got)
	}
	if _, err := Remove(p, "a@x.com", false); err != nil {
		t.Fatal(err)
	}
	got = readFile(t, p)
	if strings.Contains(got, "A changed") || !strings.Contains(got, "B body") {
		t.Errorf("removing one block disturbed the other:\n%s", got)
	}
}

func TestBodyReadsBackWhatWasWritten(t *testing.T) {
	p := tmpFile(t, "")
	body := "Host one\nPort 993\n"
	if _, err := Upsert(p, "a@x.com", body, false); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Body(p, "a@x.com")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the block was not found")
	}
	if got != body {
		t.Errorf("read back %q, wrote %q", got, body)
	}
}

// TestForeignFileIsBackedUp: the copy is the difference between an edit and a loss.
func TestForeignFileIsBackedUp(t *testing.T) {
	p := tmpFile(t, "Host mine\n")
	if _, err := Upsert(p, "a@x.com", "body\n", false); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(BackupDir())
	if err != nil {
		t.Fatalf("no backup directory: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("%d backups, want 1", len(ents))
	}
	b, err := os.ReadFile(filepath.Join(BackupDir(), ents[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "Host mine\n" {
		t.Errorf("the backup is not the original: %q", b)
	}
}

// TestOwnedFilesAreNotBackedUp: an owned file is a pure function of the registry, so a copy of it
// is a copy of something regenerable — ten per apply would be litter, not safety.
func TestOwnedFilesAreNotBackedUp(t *testing.T) {
	p := tmpFile(t, "old\n")
	if _, err := Upsert(p, "a@x.com", "body\n", true); err != nil {
		t.Fatal(err)
	}
	if ents, err := os.ReadDir(BackupDir()); err == nil && len(ents) > 0 {
		t.Errorf("an owned file was backed up %d times", len(ents))
	}
}

func TestFileLevelBlockHasNoID(t *testing.T) {
	p := tmpFile(t, "set something = 1\n")
	if _, err := Upsert(p, "", "source /somewhere/accounts.rc\n", false); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if !strings.Contains(got, "# >>> mailforge >>>") || !strings.Contains(got, "# <<< mailforge <<<") {
		t.Errorf("the file-level fences are wrong:\n%s", got)
	}
	has, err := Has(p, "")
	if err != nil || !has {
		t.Errorf("the file-level block was not found: %v %v", has, err)
	}
}

// TestUpsertTopPlacesANewBlockFirst is the ordering the keys block depends on.
//
// NeoMutt keeps the LAST binding it reads, so a managed keys block appended to the end of
// neomuttrc would override the user's own bindings.rc — the exact opposite of the intent. A new
// block therefore goes to the top; one that already exists stays where the user put it.
func TestUpsertTopPlacesANewBlockFirst(t *testing.T) {
	p := filepath.Join(t.TempDir(), "neomuttrc")
	orig := "source ~/.config/neomutt/bindings.rc\n"
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertTop(p, "keys", "source keys.rc\n", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if strings.Index(got, "source keys.rc") > strings.Index(got, "bindings.rc") {
		t.Errorf("the keys block landed after the user's bindings:\n%s", got)
	}
	if !strings.Contains(got, orig) {
		t.Errorf("the original content was not kept:\n%s", got)
	}

	// A second call must not move it, add a second one, or rewrite the file at all.
	before, _ := os.ReadFile(p)
	res, err := UpsertTop(p, "keys", "source keys.rc\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Errorf("an unchanged block was rewritten: %s", res.Note)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Errorf("the file changed on a second identical upsert")
	}
}

// TestUpsertTopLeavesAnExistingBlockWhereItIs: a program that keeps dragging a block back to the
// top is a program arguing with a user who moved it deliberately.
func TestUpsertTopLeavesAnExistingBlockWhereItIs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "neomuttrc")
	orig := "set a\n\n" + Open("keys") + "\nsource old.rc\n" + Close("keys") + "\n\nset b\n"
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertTop(p, "keys", "source new.rc\n", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if !strings.HasPrefix(got, "set a\n") {
		t.Errorf("the block was moved to the top:\n%s", got)
	}
	if !strings.Contains(got, "source new.rc") || strings.Contains(got, "source old.rc") {
		t.Errorf("the body was not replaced:\n%s", got)
	}
}

// TestUpsertTopOnAnAbsentFile: the block is the whole file, with no leading blank line.
func TestUpsertTopOnAnAbsentFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "neomuttrc")
	if _, err := UpsertTop(p, "keys", "source keys.rc\n", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), Open("keys")) {
		t.Errorf("a fresh file did not start with the block:\n%q", string(b))
	}
}

// TestOldMarkerSpellingIsFoundAndRewritten covers the one migration this program performs.
//
// Named blocks used to be fenced "# >>> mailforge account <id> >>>", from when every named block
// was an account. The keys block is not one. If the reader had been changed along with the writer,
// every block already in somebody's .mbsyncrc would have become invisible — and the next apply
// would have appended a SECOND block for the same account, leaving two IMAPStore definitions and
// an mbsync that refuses to run.
func TestOldMarkerSpellingIsFoundAndRewritten(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mbsyncrc")
	old := "# >>> mailforge account a@x.com >>>\n" + notice + "\nHost old\n" +
		"# <<< mailforge account a@x.com <<<\n"
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	if ids, err := IDs(p); err != nil || len(ids) != 1 || ids[0] != "a@x.com" {
		t.Fatalf("the old spelling was not recognised: %v %v", ids, err)
	}
	if has, _ := Has(p, "a@x.com"); !has {
		t.Errorf("Has missed a block in the old spelling")
	}

	res, err := Upsert(p, "a@x.com", "Host new\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "updated" {
		t.Errorf("the block was not updated in place: %q", res.Note)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	if strings.Count(got, "Host") != 1 {
		t.Errorf("a second block was appended:\n%s", got)
	}
	if !strings.Contains(got, Open("a@x.com")) {
		t.Errorf("it was not rewritten into the current spelling:\n%s", got)
	}
	if strings.Contains(got, "mailforge account") {
		t.Errorf("the old spelling survived:\n%s", got)
	}
}

// TestFileLevelBlockIsNotConfusedWithANamedOne: "# >>> mailforge >>>" has four fields and a named
// fence has five, so a parser reading the field before the suffix must not take "mailforge" as an id.
func TestFileLevelBlockIsNotConfusedWithANamedOne(t *testing.T) {
	if id := idOf("# >>> mailforge >>>"); id != "" {
		t.Errorf("the file-level block got the id %q", id)
	}
	if id := idOf("# >>> mailforge keys >>>"); id != "keys" {
		t.Errorf("a named block parsed as %q", id)
	}
	if id := idOf("# >>> mailforge account a@x.com >>>"); id != "a@x.com" {
		t.Errorf("the old spelling parsed as %q", id)
	}
}
