// Package edit is the only place in mailforge that touches a file it does not own.
//
// It knows nothing about mail. A fenced block is an id and some lines; everything outside the
// fences is somebody else's and is preserved byte for byte. That narrowness is the point: if a
// foreign file is ever damaged, the bug is in this package and nowhere else.
//
// The engine REFUSES rather than guesses. A file whose fence structure is not exactly sane —
// an open without a close, a close before an open, the same id twice, fences inside fences — is a
// file this program does not understand, and writing into a half-understood configuration is the
// one thing it must never do. The error names the line numbers and stops the whole apply.
package edit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lvim-tech/mailforge/internal/config"
)

// Fence lines. The id appears in BOTH, so every block is self-describing and an unbalanced one is
// detectable per id rather than only as "something is wrong in this file".
const (
	openPrefix  = "# >>> mailforge"
	closePrefix = "# <<< mailforge"
	openSuffix  = ">>>"
	closeSuffix = "<<<"

	// notice is the second line of every block: the only honest way to make "do not edit"
	// believable is to say what will happen instead.
	notice = "# Managed by mailforge. Everything between these markers is rewritten on apply; " +
		"edits here are lost."
)

// Open returns the opening fence for an id. An empty id is the file-level block — the single
// include line mailforge keeps inside the user's own neomuttrc.
//
// The id goes in as itself. It used to be spelled "account <id>", from when every named block was
// an account; the keys block is not one, and a fence reading "mailforge account keys" in somebody's
// neomuttrc is a lie about what the block is for. idOf still READS the old spelling, so blocks
// already in a user's .mbsyncrc are found and rewritten into the new one on the next apply rather
// than orphaned into duplicates.
func Open(id string) string {
	if id == "" {
		return openPrefix + " " + openSuffix
	}
	return openPrefix + " " + id + " " + openSuffix
}

// Close returns the closing fence for an id.
func Close(id string) string {
	if id == "" {
		return closePrefix + " " + closeSuffix
	}
	return closePrefix + " " + id + " " + closeSuffix
}

// Result says what an operation did, so the caller can report it rather than assume.
type Result struct {
	Changed bool   // false means the file already said exactly this
	Created bool   // the file did not exist
	Note    string // human-readable, for the apply log
}

// block is where an id's fences were found: line indices, inclusive of the fences themselves.
type block struct {
	id         string
	open, shut int
}

// scan finds every fenced block, and refuses anything it cannot read with certainty.
func scan(lines []string) ([]block, error) {
	var (
		found   []block
		openIdx = -1
		openID  string
	)
	for i, raw := range lines {
		l := strings.TrimRight(raw, " \t\r")
		switch {
		case isOpen(l):
			if openIdx >= 0 {
				return nil, fmt.Errorf("line %d: %s opens inside %s, opened on line %d",
					i+1, label(idOf(l)), label(openID), openIdx+1)
			}
			openIdx, openID = i, idOf(l)
		case isClose(l):
			if openIdx < 0 {
				return nil, fmt.Errorf("line %d: a block closes that never opened", i+1)
			}
			if got := idOf(l); got != openID {
				return nil, fmt.Errorf("line %d: %s is closed by the marker for %s",
					i+1, label(openID), label(got))
			}
			found = append(found, block{id: openID, open: openIdx, shut: i})
			openIdx, openID = -1, ""
		}
	}
	if openIdx >= 0 {
		return nil, fmt.Errorf("line %d: %s is never closed", openIdx+1, label(openID))
	}
	seen := map[string]int{}
	for _, b := range found {
		if prev, dup := seen[b.id]; dup {
			return nil, fmt.Errorf("line %d: %s is fenced a second time; the first is on line %d",
				b.open+1, label(b.id), prev+1)
		}
		seen[b.id] = b.open
	}
	return found, nil
}

// label names a block in an error.
//
// The file-level block has no id, and printing it as %q gave `""` — which reads as a bug in the
// message rather than a description of the block, and tells somebody looking for the offending
// lines nothing about which ones to look for.
func label(id string) string {
	if id == "" {
		return "the file-level block"
	}
	return "the block for " + strconv.Quote(id)
}

func isOpen(l string) bool {
	return strings.HasPrefix(l, openPrefix) && strings.HasSuffix(l, openSuffix)
}

func isClose(l string) bool {
	return strings.HasPrefix(l, closePrefix) && strings.HasSuffix(l, closeSuffix)
}

// idOf pulls the id out of a fence line, or "" for the file-level block.
func idOf(l string) string {
	f := strings.Fields(l)
	// "# >>> mailforge <id> >>>" is five fields; the file-level form is four.
	//
	// The six-field "# >>> mailforge account <id> >>>" is the older spelling, still read so that
	// a file written by an earlier build keeps working — it is found under the same id and the
	// next write puts it back in the current form.
	if len(f) >= 6 && f[3] == "account" {
		return strings.Join(f[4:len(f)-1], " ")
	}
	if len(f) >= 5 {
		return strings.Join(f[3:len(f)-1], " ")
	}
	return ""
}

// Upsert puts body between the fences for id, creating the file if it is not there.
//
// Byte-for-byte no change means NO WRITE: no backup, no new mtime, nothing. That is what makes
// apply safe to run as often as you like — the second run of an unchanged setup must be provably
// inert, or nobody will run it.
func Upsert(path, id, body string, owned bool) (Result, error) {
	return upsert(path, id, body, owned, false)
}

// UpsertTop is Upsert for a block that must be read BEFORE the rest of the file.
//
// Only the placement of a NEW block differs; one that already exists is updated where it stands,
// because the user may have moved it and a program that keeps dragging a block back to the top is
// a program arguing with its user. The distinction exists for the keys block: bindings sourced
// later win, so this program's have to come first for the user's own to override them.
func UpsertTop(path, id, body string, owned bool) (Result, error) {
	return upsert(path, id, body, owned, true)
}

func upsert(path, id, body string, owned bool, top bool) (Result, error) {
	orig, existed, err := read(path)
	if err != nil {
		return Result{}, err
	}
	lines := split(orig)

	blocks, err := scan(lines)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w\n  fix it by hand, then apply again", path, err)
	}

	fresh := renderBlock(id, body)
	var out string
	if b, ok := find(blocks, id); ok {
		before := joinLines(lines[:b.open])
		after := joinLines(lines[b.shut+1:])
		out = before + fresh + after
	} else if top {
		out = fresh
		if orig != "" {
			out += "\n" + orig
		}
	} else {
		out = orig
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		if out != "" {
			out += "\n"
		}
		out += fresh
	}

	if out == orig {
		return Result{Changed: false, Note: "already up to date"}, nil
	}
	if err := write(path, out, existed, owned); err != nil {
		return Result{}, err
	}
	note := "updated"
	if !existed {
		note = "created"
	} else if _, ok := find(blocks, id); !ok {
		note = "added"
	}
	return Result{Changed: true, Created: !existed, Note: note}, nil
}

// Remove deletes an id's block. An absent block is success — the caller asked for a state, and
// that state already holds.
func Remove(path, id string, owned bool) (Result, error) {
	orig, existed, err := read(path)
	if err != nil {
		return Result{}, err
	}
	if !existed {
		return Result{Changed: false, Note: "file was not there"}, nil
	}
	lines := split(orig)
	blocks, err := scan(lines)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w\n  fix it by hand, then apply again", path, err)
	}
	b, ok := find(blocks, id)
	if !ok {
		return Result{Changed: false, Note: "was not present"}, nil
	}

	// Take the blank line that was inserted with the block, so repeated add/remove cycles do
	// not leave a growing stack of empty lines behind them.
	from := b.open
	if from > 0 && strings.TrimSpace(lines[from-1]) == "" {
		from--
	}
	out := joinLines(lines[:from]) + joinLines(lines[b.shut+1:])
	if out == orig {
		return Result{Changed: false, Note: "already up to date"}, nil
	}
	if err := write(path, out, true, owned); err != nil {
		return Result{}, err
	}
	return Result{Changed: true, Note: "removed"}, nil
}

// Has reports whether the file carries a block for id, and surfaces a broken structure rather
// than answering as if it were understood.
func Has(path, id string) (bool, error) {
	orig, existed, err := read(path)
	if err != nil || !existed {
		return false, err
	}
	blocks, err := scan(split(orig))
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	_, ok := find(blocks, id)
	return ok, nil
}

// Body returns what is currently between the fences for id, for comparing against a fresh render.
func Body(path, id string) (string, bool, error) {
	orig, existed, err := read(path)
	if err != nil || !existed {
		return "", false, err
	}
	lines := split(orig)
	blocks, err := scan(lines)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", path, err)
	}
	b, ok := find(blocks, id)
	if !ok {
		return "", false, nil
	}
	// Skip the fence and the notice line beneath it.
	start := b.open + 1
	if start < b.shut && strings.HasPrefix(strings.TrimSpace(lines[start]), "# Managed by mailforge") {
		start++
	}
	return joinLines(lines[start:b.shut]), true, nil
}

func find(blocks []block, id string) (block, bool) {
	for _, b := range blocks {
		if b.id == id {
			return b, true
		}
	}
	return block{}, false
}

func renderBlock(id, body string) string {
	var b strings.Builder
	b.WriteString(Open(id) + "\n")
	b.WriteString(notice + "\n")
	if body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString(Close(id) + "\n")
	return b.String()
}

func read(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(b), true, nil
}

// split keeps the file's line structure without losing a trailing newline: joinLines puts back
// exactly what was taken apart.
func split(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// write replaces the file atomically, backing up a foreign one first.
//
// Owned files get no backup: they are a pure function of the registry, so a copy of one is a copy
// of something regenerable, and ten of them per apply is litter rather than safety.
func write(path, content string, existed, owned bool) error {
	if existed && !owned {
		if err := backup(path); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o600)
	if existed {
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return config.WriteAtomic(path, []byte(content), mode)
}

// BackupDir is where copies of foreign files go before they are changed.
func BackupDir() string {
	home, _ := os.UserHomeDir()
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "mailforge", "backups")
	}
	return filepath.Join(home, ".local", "state", "mailforge", "backups")
}

const keepBackups = 10

func backup(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dir := BackupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := filepath.Base(path)
	name := fmt.Sprintf("%s.%s", base, time.Now().UTC().Format("20060102T150405Z"))
	if err := config.WriteAtomic(filepath.Join(dir, name), b, 0o600); err != nil {
		return err
	}
	return prune(dir, base)
}

// prune keeps the last few copies per file. Without it a program that is safe to run repeatedly
// fills a directory with identical files, and the safety turns into a mess nobody cleans.
func prune(dir, base string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var mine []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), base+".") {
			mine = append(mine, e.Name())
		}
	}
	if len(mine) <= keepBackups {
		return nil
	}
	sort.Strings(mine) // the stamp is lexicographically ordered by design
	for _, name := range mine[:len(mine)-keepBackups] {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}

// IDs lists every fenced block in a file, so a caller can find the ones nothing claims any more.
func IDs(path string) ([]string, error) {
	orig, existed, err := read(path)
	if err != nil || !existed {
		return nil, err
	}
	blocks, err := scan(split(orig))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, b.id)
	}
	return out, nil
}
