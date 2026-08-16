// Package store is mailforge's bridge to `pass`, the standard Unix password manager.
//
// Ported from keyforge's internal/passstore rather than rewritten. The care in it — the hand-made
// pipe, the locked buffer, the refusal to let a password become an argument or an unwipeable Go
// string — is reviewed code solving a problem both programs have, and a second implementation
// would be a second set of edge cases around the same secret.
//
// WHY MAILFORGE DOES NOT STORE ANYTHING ITSELF. A password store is a small idea with a large
// surface: key derivation, an on-disk format, atomic writes, memory that must not be swapped or
// dumped, a master secret whose handling decides everything. Writing another one would be the
// opposite of "a layer over the tools" — it would be a new, unreviewed piece of cryptography sitting
// under the very secrets it claims to protect. `pass` already exists, is GPG underneath, and is on
// this machine. mailforge writes into it and gets out of the way.
//
// WHAT THIS BUYS, AND WHAT IT DOES NOT. The store is ciphertext at rest: a stolen disk, a backup, a
// second user on the machine — none of them get anything. What it cannot survive is root on the
// same machine WHILE the secret is unlocked, because at that moment the plaintext exists in a
// process and the agent socket answers to whoever can reach it. That is not a flaw in `pass`; it is
// true of ssh-agent, of gnome-keyring, and of anything that could be written here. The only design
// that beats it keeps the key off the machine entirely, on a hardware token.
package store

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lvim-tech/mailforge/internal/sys"
)

// Available reports whether `pass` is installed and initialised.
func Available() bool {
	if !sys.Have("pass") {
		return false
	}
	_, err := os.Stat(filepath.Join(dir(), ".gpg-id"))
	return err == nil
}

// dir is the password store location, honouring PASSWORD_STORE_DIR like pass itself.
func dir() string {
	if d := os.Getenv("PASSWORD_STORE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".password-store"
	}
	return filepath.Join(home, ".password-store")
}

// Dir is the store location, for showing where entries actually live.
func Dir() string { return dir() }

// Recipient returns the PRIMARY GPG id the store encrypts to.
//
// The first line, not the whole file. `.gpg-id` holds one id per line — `pass init a b` is
// ordinary on a shared store — and returning all of them joined by newlines was fed straight
// into `gpg --encrypt --recipient` as ONE id, which broke the lock's challenge and the
// encryption-keygrip lookup on exactly those stores. Every caller here wants the primary one.
func Recipient() string {
	for _, r := range Recipients() {
		return r
	}
	return ""
}

// Recipients returns every id in .gpg-id, for the places that must not pretend there is one.
func Recipients() []string {
	b, err := os.ReadFile(filepath.Join(dir(), ".gpg-id"))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Entry is one record: its full store path, the folder it sits in, and when it was last written.
//
// The split is kept here rather than in the view because the folder is not decoration — it is how
// a store with a hundred site logins stays readable, and every caller wants the same split.
type Entry struct {
	Name     string // full path in the store, without the .gpg suffix
	Folder   string // "" for a top-level entry
	Leaf     string // the last segment
	Modified time.Time
}

// Entries returns every record in the store, sorted by path.
//
// Only names and timestamps are read; nothing is decrypted, so this never prompts for a passphrase
// and never touches a secret. That is what makes it safe to run on startup for a tab the user may
// never open.
func Entries() ([]Entry, error) {
	root := dir()
	var out []Entry
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// The store is frequently a git repository; its object database is not password data.
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == ".gnupg" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".gpg") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), ".gpg")
		folder, leaf := path.Split(name)
		out = append(out, Entry{
			Name:     name,
			Folder:   strings.TrimSuffix(folder, "/"),
			Leaf:     leaf,
			Modified: info.ModTime(),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// Exists reports whether an entry is already there, so a generated secret never silently replaces
// one that is still in use somewhere.
func Exists(name string) bool {
	_, err := os.Stat(filepath.Join(dir(), filepath.FromSlash(name)+".gpg"))
	return err == nil
}

// ValidName rejects a path that would escape the store or produce an unreachable entry.
//
// `pass` joins the name onto the store directory without much argument, so "../../.ssh/id_ed25519"
// is a file it will cheerfully encrypt over. The check belongs here, at the one place a name turns
// into a path, rather than in whichever form happens to be collecting it.
func ValidName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty entry name")
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("the name is a path inside the store, not on disk — drop the leading slash")
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" {
			return fmt.Errorf("empty segment in path %q", name)
		}
		if seg == "." || seg == ".." {
			return fmt.Errorf("%q leads outside the store", name)
		}
	}
	return nil
}

// existence is what a write requires the store to look like: the guarantee Insert and Replace each
// make, in the one form write can re-check for itself.
type existence int

const (
	mustNotExist existence = iota
	mustExist
)

func (e existence) check(name string) error {
	switch {
	case e == mustNotExist && Exists(name):
		return fmt.Errorf("entry %q already exists — pick another name", name)
	case e == mustExist && !Exists(name):
		return fmt.Errorf("no entry %q", name)
	}
	return nil
}

// InsertSecret writes a secret held in locked memory. It refuses to overwrite.
//
// This is the path a password the user typed takes: from the mlocked buffer, through a pipe, into
// gpg. See write below for why that pipe is built by hand.
func InsertSecret(name string, secret *sys.Secret, meta map[string]string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	return write(name, mustNotExist, func(w io.Writer) error {
		_, err := secret.WriteTo(w)
		return err
	}, renderMeta(meta))
}

// Replace overwrites an existing entry. It refuses to CREATE one, which is the mirror of Insert:
// between them, no single call can both fail to find what you meant and silently do the other thing.
func Replace(name string, secret *sys.Secret, meta map[string]string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	return write(name, mustExist, func(w io.Writer) error {
		_, err := secret.WriteTo(w)
		return err
	}, renderMeta(meta))
}

// write runs `pass insert --multiline` and feeds it the body through a pipe of our own.
//
// The stdin is an *os.File and that is the entire reason this is not three lines. Handed any other
// reader, os/exec starts a goroutine that copies through a 32 KB buffer on the Go heap — a copy of
// the password that cannot be wiped and that the collector is free to duplicate while compacting.
// An *os.File is passed to the child as a file descriptor, so the bytes travel from the locked
// buffer into the kernel's pipe and touch nothing in between.
//
// --force is always given. Not to overwrite blindly — Insert and Replace have already decided that
// question in Go, where the answer can be reported properly — but because without it `pass` asks
// for confirmation on stdin, and the "y" it is waiting for would be read out of the password.
//
// And because --force obeys whatever it finds, `want` is re-checked HERE, as late as it can be.
// The caller's check happened before a decryption that may have waited on a pinentry; a second
// mailforge, or a git pull into the store, can create or delete the entry inside that window and
// turn "refuses to overwrite" into an overwrite with no error anywhere. This does not close the
// race — only a lock on the store would — but it narrows it from seconds to microseconds and makes
// the collision loud instead of silent.
func write(name string, want existence, secret func(io.Writer) error, trailer string) error {
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	cmd := exec.Command("pass", "insert", "--multiline", "--force", name)
	cmd.Stdin = pr
	errb := &strings.Builder{}
	cmd.Stderr = errb
	cmd.Stdout = nil

	if err := want.check(name); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return fmt.Errorf("pass insert: %w", err)
	}
	// The child holds its own descriptor now; ours must go, or the read end never sees EOF.
	_ = pr.Close()

	werr := writeBody(pw, secret, trailer)
	_ = pw.Close()

	if err := cmd.Wait(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("pass insert: %s", msg)
	}
	return werr
}

// writeBody lays out the entry: the password on the first line, everything else below it. That
// order is the convention the rest of the pass ecosystem reads — `pass show -c` copies line one and
// nothing else, so anything written above the password would be copied instead of it.
//
// Below the password it writes RAW TEXT rather than rendering a map. An entry's lower half is not
// necessarily a set of fields: it may hold a free-form note, or an `otpauth://` line that carries a
// second factor. A writer that only understood `key: value` would drop those on the way through,
// which is exactly the loss this signature exists to prevent.
func writeBody(w io.Writer, secret func(io.Writer) error, trailer string) error {
	if err := secret(w); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if trailer == "" {
		return nil
	}
	if !strings.HasSuffix(trailer, "\n") {
		trailer += "\n"
	}
	_, err := io.WriteString(w, trailer)
	return err
}

// renderMeta turns fields into the lower half of an entry.
//
// Kept as a convenience for callers that genuinely have a map, above the raw layer rather than
// inside it, so there is exactly one representation at the point of writing.
func renderMeta(meta map[string]string) string {
	// Deterministic order, so the same inputs produce the same file and a git-backed store shows
	// meaningful diffs instead of reordered noise.
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		v := strings.TrimSpace(meta[k])
		if v == "" {
			continue
		}
		// A newline in a value would forge an extra field; a field is one line by definition.
		v = strings.ReplaceAll(v, "\n", " ")
		fmt.Fprintf(&b, "%s: %s\n", k, v)
	}
	return b.String()
}

// Trailer returns everything below an entry's password, verbatim.
//
// The whole record must be decrypted to read any of it, so the password necessarily passes through
// this process; it is WIPED in the locked buffer before anything is read out, exactly as Fields
// does. What remains is not a secret, which is what makes returning it as an ordinary string
// legitimate. Unlike Fields it parses nothing, because its purpose is to put the text back
// unchanged rather than to understand it.
func Trailer(name string) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	sec, err := decrypt(name)
	if err != nil {
		return "", err
	}
	defer sec.Close()

	sec.DropThrough('\n')
	var out string
	sec.Use(func(rest string) { out = rest })
	return out, nil
}

// ReplacePassword changes an entry's password and puts everything below it back untouched.
//
// The plain Replace writes the body it is given and nothing else, which is right when the caller
// owns the whole entry and wrong when it owns only the password. mailforge is the second case: an
// address may already have an entry somebody else wrote — with a login, a URL, a note — and
// replacing that wholesale to change one line is silent data loss the user finds out about long
// afterwards, from the field that is no longer there.
func ReplacePassword(name string, secret *sys.Secret) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if !Exists(name) {
		return fmt.Errorf("no entry %q", name)
	}
	trailer, err := Trailer(name)
	if err != nil {
		return err
	}
	return write(name, mustExist, func(w io.Writer) error {
		_, err := secret.WriteTo(w)
		return err
	}, trailer)
}

// Remove deletes an entry. Nothing here asks whether you are sure — the view does that, where the
// name being deleted is on screen.
func Remove(name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if !Exists(name) {
		return fmt.Errorf("no entry %q", name)
	}
	res, err := sys.Run("pass", "rm", "--force", name)
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("pass rm: %s", firstLine(res.Stderr, "delete failed"))
	}
	return nil
}

// Reveal decrypts an entry and returns its password in locked memory.
//
// This is the one call in the package that hands the password back, and it is built so that doing
// so costs as little as it can. `pass show` writes into a pipe this package owns, and the bytes are
// read straight into an mlocked buffer: no heap string, nothing for the collector to duplicate,
// nothing that cannot be wiped. The caller must Close the result.
//
// It exists because a password you can only copy is a password you cannot read off the screen to
// type into a phone, and that turned out to be a real thing people need. It is still the weaker
// path — the value ends up on a screen — which is why the view puts it behind its own key, hides it
// again on a timer, and never turns it on by itself.
func Reveal(name string) (*sys.Secret, error) {
	sec, err := decrypt(name)
	if err != nil {
		return nil, err
	}
	// Only line one is the password; the metadata below it is dropped inside the locked buffer.
	sec.TruncateAt('\n')
	return sec, nil
}

// decrypt reads a whole entry into locked memory, and is the ONLY path by which a decrypted
// entry enters this process.
//
// Split out of Reveal because Fields used to take the other road — sys.Run, whose captured
// stdout is an ordinary Go string — and so quietly reintroduced the password into heap memory
// that nothing can wipe, on every detail view and every edit. The pipe exists precisely so
// that never happens; having two ways in meant only one of them was true.
func decrypt(name string) (*sys.Secret, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe: %w", err)
	}
	cmd := exec.Command("pass", "show", name)
	cmd.Stdout = pw
	errb := &strings.Builder{}
	cmd.Stderr = errb
	// No stdin, for the same reason Run has none: with the agent locked, pinentry must fail rather
	// than block behind a TUI that has already painted over its question.
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, fmt.Errorf("pass show: %w", err)
	}
	// Our copy of the write end must go, or the read below never sees EOF.
	_ = pw.Close()

	// 64 KB, not the 4 KB a password would need. What is read here is the WHOLE entry — the
	// password on line one and whatever the user keeps under it, which is legitimately recovery
	// codes or an otpauth blob — and ReplacePassword writes that trailer back. Sizing the buffer
	// for the password alone made the rewrite drop everything past 4 KB, silently, from the one
	// file whose contents nobody can reconstruct.
	sec, err := sys.NewSecret(64 * 1024)
	if err != nil {
		_ = pr.Close()
		_ = cmd.Wait()
		return nil, err
	}
	_, rerr := sec.ReadFrom(pr)
	_ = pr.Close()

	if err := cmd.Wait(); err != nil {
		sec.Close()
		return nil, fmt.Errorf("%s", firstLine(errb.String(), "gpg did not unlock the entry"))
	}
	if rerr != nil {
		sec.Close()
		return nil, rerr
	}
	// A truncated read must never reach a caller that writes the value back: it would re-encrypt
	// the entry with its tail missing and report success.
	if sec.Overflowed() {
		sec.Close()
		return nil, fmt.Errorf("the entry %q is larger than this program will decrypt (64 KB); "+
			"it was left untouched", name)
	}
	return sec, nil
}

// firstLine reduces a tool's complaint to the part worth showing on a one-line status bar.
func firstLine(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
