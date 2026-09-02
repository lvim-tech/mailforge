// Package config holds mailforge's own settings — where it puts things, not what any account is.
//
// The line between this file and internal/account is worth stating once: an account describes a
// mailbox on somebody else's server, and lives in accounts.json; this describes where THIS machine
// keeps its mail and its configuration, and lives in config.json. Nothing here is a secret, which
// is why it may be an ordinary JSON file with ordinary permissions while the passwords go to pass.
//
// MAXIMAL CONFIGURABILITY. Everything a rendered file hardcodes is either a field below or derived
// from an account. A literal in the renderer that somebody could reasonably want different is a
// bug in this file, not a style choice there.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/lvim-tech/mailforge/internal/keys"
)

// PassLayout names one of the two shapes a password entry can have beneath PassPrefix.
//
// Two, and not an arbitrary template, on purpose. A format string would let the name be anything,
// and the value has to be produced identically in four places — the mbsync block, the NeoMutt rc,
// the audit and the wizard — from data that is itself user-supplied. Two named shapes can be
// enumerated, which is what lets mailforge look for an entry in the OTHER one when the configured
// one is empty, and say so when both are occupied.
type PassLayout string

const (
	// LayoutFlat is <prefix>/<address> — mailforge's own historical shape.
	LayoutFlat PassLayout = "flat"
	// LayoutDomain is <prefix>/<domain>/<address> — how keyforge files a store.
	LayoutDomain PassLayout = "domain"
)

// Other is the layout this one is not. The fallback search and the duplicate check are both just
// "look at the other one", and naming that here keeps the pairing in a single place.
func (l PassLayout) Other() PassLayout {
	if l == LayoutDomain {
		return LayoutFlat
	}
	return LayoutDomain
}

// Valid reports whether l is one of the two known shapes.
func (l PassLayout) Valid() bool { return l == LayoutFlat || l == LayoutDomain }

// SyncDefaults are the Channel options every rendered mbsync block carries.
//
// They are here rather than in the renderer because they are policy, not format: whether a
// deletion on the server deletes locally is a decision about how this person wants their mail to
// behave, and it belongs where decisions live.
type SyncDefaults struct {
	// Create says which side may create mailboxes the other has. "Both" is what a first sync of
	// an existing account needs; "Near" refuses to create anything on the server.
	Create string `json:"create"`
	// Expunge says which side actually deletes messages marked deleted.
	Expunge string `json:"expunge"`
	// MaxMessages caps how many messages a mailbox keeps locally. 0 means all of them.
	MaxMessages int `json:"max_messages"`
	// ExpireUnread lets the cap delete unread mail too. Off, because a cap that silently
	// removes something never read is a cap that loses mail.
	ExpireUnread bool `json:"expire_unread"`
}

// Config is everything mailforge remembers about this machine.
type Config struct {
	// MaildirRoot is where mbsync writes and NeoMutt reads. One directory per address.
	MaildirRoot string `json:"maildir_root"`
	// NeomuttDir holds neomuttrc and the mailforge-owned subtree beneath it.
	NeomuttDir string `json:"neomutt_dir"`
	// MbsyncRC is the foreign file mailforge edits between markers, and the only one.
	MbsyncRC string `json:"mbsync_rc"`
	// PassPrefix is the folder in `pass` that account passwords live under.
	PassPrefix string `json:"pass_prefix"`
	// PassLayout is how an address becomes a name beneath that folder.
	//
	// It exists because the store is not mailforge's to shape. `pass` is shared with whatever
	// else a person keeps in it, and a store maintained by keyforge is filed by domain —
	// mail/example.com/hello@example.com — while mailforge's own older default was flat.
	// Reading the wrong one gets "is not in the password store" out of mbsync, which reads like
	// a password problem rather than a path problem and sends people looking in the wrong place.
	//
	// The alternative was for mailforge to write its own copy under the name it preferred. That
	// is what it used to do, and it is wrong twice over: two encrypted copies of one secret
	// drift the moment either is rotated, and neither file says which one is authoritative.
	PassLayout PassLayout `json:"pass_layout"`
	// CertFile is the CA bundle mbsync is told to trust. Distributions disagree about the path
	// and getting it wrong fails at connection time with a message about certificates that
	// sounds like the server's fault.
	CertFile string `json:"cert_file"`
	// RealName is the default display name offered when adding an account.
	RealName string `json:"real_name"`

	// Editor is what NeoMutt opens to write a message.
	//
	// A SETTING rather than a reference to $EDITOR, because the seeded `set editor = "$EDITOR"`
	// was expanded once, when neomuttrc was parsed, out of whatever environment happened to
	// start NeoMutt. Launched from anywhere that does not export it — a desktop entry, a
	// terminal that runs a login-less shell — the value came out empty and composing a message
	// ended in "error running $EDITOR", which names the variable rather than the fact that it
	// was not there. A path in the config is a path on every start.
	Editor string `json:"editor"`

	// SidebarCounts turns on NeoMutt's mail_check_stats.
	//
	// The seeded sidebar_format asks for %N and %S — new and total — and those two are computed
	// only when this is on. Off, which is NeoMutt's default, the sidebar counts the mailbox that
	// happens to be open and shows nothing for the rest, which reads as a broken sidebar rather
	// than as a setting nobody turned on. mailforge is the one that chose that format, so it is
	// the one that has to enable what the format needs.
	SidebarCounts bool `json:"sidebar_counts"`

	// The three timers behind "when does the sidebar catch up", kept SEPARATE on purpose.
	//
	// NeoMutt nests them: it waits WakeEvery for a key, then looks for mail if CheckEvery has
	// elapsed, and only inside that look recomputes the counts if SidebarCountsEvery has. Each
	// is a ceiling over the next — which is exactly why one number would be the wrong answer:
	// a recount is real work on a large maildir and a new-mail check is not, so somebody with
	// forty thousand messages wants to check often and count rarely. Collapsing them into one
	// takes that away.
	//
	// What the nesting DOES require is that the interface show the effective value, so a one
	// second recount under a five second wake reads as "every 5 s" rather than as a setting
	// that quietly does nothing.

	// SidebarCountsEvery is how often the counts are recomputed, in seconds.
	SidebarCountsEvery int `json:"sidebar_counts_every"`

	// CheckEvery is NeoMutt's mail_check: the least time between two looks for new mail.
	//
	// Managed here because the seed put 60 in the user's own settings.rc — twelve times slower
	// than NeoMutt's own default of 5 — and being in their file it beat anything this program
	// could set.
	CheckEvery int `json:"check_every_seconds"`

	// WakeEvery is NeoMutt's timeout: how long it waits for a keypress before doing the work
	// above. The outermost ceiling of the three.
	WakeEvery int `json:"wake_every_seconds"`

	// SyncEvery is how often a systemd user timer runs `mbsync -a`, IN SECONDS. Zero means no
	// timer, which is the state of a machine where nothing fetches mail on its own.
	//
	// A number rather than a switch, because "on" without an interval is not an answer: four
	// accounts every two minutes and four accounts every half hour are different agreements
	// with somebody else's mail server.
	//
	// Seconds rather than minutes as the stored unit, so a sub-minute interval is expressible
	// at all. The interface takes and shows it in whatever reads best — "30 s", "5 min" — and
	// keeps one canonical number underneath, because two units in the file is two chances to
	// read the wrong one.
	SyncEvery int `json:"sync_every_seconds"`

	// FetchWhenLoggedOut asks systemd to keep this user's services running after logout —
	// `loginctl enable-linger`.
	//
	// Without it a user timer lives exactly as long as the login session, so a machine left on
	// overnight with nobody logged in fetches nothing, and the mail is waiting in the morning
	// instead of downloaded. With it, the fetch keeps running on a machine you walked away
	// from — which is a decision about the machine, not about mail, and so is asked rather
	// than assumed.
	FetchWhenLoggedOut bool `json:"fetch_when_logged_out"`
	// Sync carries the Channel policy above.
	Sync SyncDefaults `json:"sync"`
	// VerifyTimeoutSec bounds each stage of the live check. A dead host must fail in seconds,
	// not hang the interface until the operating system gives up.
	VerifyTimeoutSec int `json:"verify_timeout_sec"`
	// Keys maps an action name from internal/keys to the key that runs it. Only the mapping —
	// which NeoMutt function the name means lives in that package, so this file never has to
	// carry muttrc syntax.
	//
	// An empty value means deliberately unbound, which is why an absent entry and an empty one
	// are not the same thing and Fill distinguishes them.
	Keys map[string]string `json:"keys"`
	// ISPDB allows discovery to ask Mozilla's database about an unknown domain.
	//
	// It is a setting because it is a privacy decision, not a technical one: the query tells
	// Mozilla which mail domain this machine is being configured for. Off means discovery uses
	// only the built-in table, DNS and a direct probe of the domain's own servers.
	ISPDB bool `json:"ispdb"`

	// Theme names the palette mailforge's interface paints with: a theme name, or a path to a
	// palette file. Empty means whatever lvim-colorscheme currently says is active, and the
	// built-in Everforest when that resolves to nothing — see internal/theme for the order and
	// why it is that order. This is the ONE setting here that is about how the program looks
	// rather than where it writes; it is kept beside the rest because config.json is where a
	// user already reaches to change how mailforge behaves.
	Theme string `json:"theme,omitempty"`
}

// Default is what a machine with no config file behaves like.
//
// The paths are the XDG ones rather than the older single-tree shape, because config, mail data
// and caches have different lifetimes and different backup answers: the first is worth keeping in
// version control, the second is large and re-fetchable, the third is disposable. A tree that
// mixes them makes every one of those questions harder to answer.
func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		MaildirRoot: filepath.Join(home, ".local", "share", "mail"),
		NeomuttDir:  filepath.Join(home, ".config", "neomutt"),
		MbsyncRC:    filepath.Join(home, ".mbsyncrc"),
		PassPrefix:  "mail",
		// Flat, because every store mailforge has written so far is flat and a default that
		// silently relocated them would be a migration nobody asked for. A domain-filed store
		// is still found without changing this: the fallback looks in the other layout.
		PassLayout: LayoutFlat,
		CertFile:   "/etc/ssl/ca-bundle.pem",
		RealName:   "",
		Editor:     "vim",
		// On, and every minute. The cost is a stat of each maildir on a timer — cheap, because
		// mbsync fetches into local directories and NeoMutt never asks a server for this.
		SidebarCounts:      true,
		SidebarCountsEvery: 5,
		CheckEvery:         10,
		WakeEvery:          5,
		SyncEvery:          300,
		Sync:               SyncDefaults{Create: "Both", Expunge: "Both", MaxMessages: 0},
		VerifyTimeoutSec:   10,
		ISPDB:              true,
		Keys:               keys.Defaults(),
	}
}

// Same reports whether two configurations are identical.
//
// A method rather than `==` because Config carries a map now, and Go refuses to compare those. One
// definition, so "has this screen been changed" is answered the same way everywhere.
func Same(a, b Config) bool { return reflect.DeepEqual(a, b) }

// Path is the config file location, honouring XDG_CONFIG_HOME.
func Path() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "mailforge", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "mailforge.json"
	}
	return filepath.Join(home, ".config", "mailforge", "config.json")
}

// ThemeDir is where mailforge looks for palette files, beside the config they belong with. One
// directory of small documents rather than colours inside config.json: a theme is generated by
// another tool and dropped in, and generated files have no business being merged into hand-edited
// settings. Honours XDG_CONFIG_HOME the same way Path does.
func ThemeDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "mailforge", "themes")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "mailforge-themes"
	}
	return filepath.Join(home, ".config", "mailforge", "themes")
}

// Exists reports whether this machine has been set up. The first-run screen turns on this.
func Exists() bool {
	_, err := os.Stat(Path())
	return err == nil
}

// Load reads the config, filling absent fields from Default.
//
// A file that cannot be parsed is REPORTED rather than replaced. Settings that quietly reset are
// how a machine ends up rendering to paths the user stopped using, and the sentence "it worked
// yesterday" starts there.
func Load() (Config, error) {
	c := Default()
	b, err := os.ReadFile(Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Default(), err
	}
	return c.withDefaults(), nil
}

// withDefaults fills anything absent or nonsensical, so a hand-edited file with three fields in it
// still produces a complete configuration rather than a program that writes to "".
func (c Config) withDefaults() Config {
	d := Default()
	if c.MaildirRoot == "" {
		c.MaildirRoot = d.MaildirRoot
	}
	if c.NeomuttDir == "" {
		c.NeomuttDir = d.NeomuttDir
	}
	if c.MbsyncRC == "" {
		c.MbsyncRC = d.MbsyncRC
	}
	if c.PassPrefix == "" {
		c.PassPrefix = d.PassPrefix
	}
	// An unknown value is treated as absent rather than refused: config.json is hand-editable,
	// and a typo in one field should not stop mailforge from starting.
	if !c.PassLayout.Valid() {
		c.PassLayout = d.PassLayout
	}
	if c.CertFile == "" {
		c.CertFile = d.CertFile
	}
	if c.Sync.Create == "" {
		c.Sync.Create = d.Sync.Create
	}
	if c.Sync.Expunge == "" {
		c.Sync.Expunge = d.Sync.Expunge
	}
	if c.Sync.MaxMessages < 0 {
		c.Sync.MaxMessages = 0
	}
	if c.VerifyTimeoutSec <= 0 {
		c.VerifyTimeoutSec = d.VerifyTimeoutSec
	}
	// Per-action, not all-or-nothing: a file written before an action existed must gain that
	// one default without losing the twenty bindings the user chose.
	c.Keys = keys.Fill(c.Keys)
	return c
}

// Save writes the config atomically.
//
// Temp file in the same directory, then rename: a rename within one filesystem either happens or
// does not, so a crash mid-write leaves the old file whole instead of half a JSON document that
// the next start refuses to parse. The same discipline every file this program writes gets.
func Save(c Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(p, append(b, '\n'), 0o600)
}

// WriteAtomic replaces a file's contents in one step, or not at all.
//
// Exported because every writer in mailforge needs it and there must be exactly one of it: a
// second implementation is a second set of edge cases, and the edge case here is somebody's mail
// configuration.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename has succeeded

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	// Sync before rename. Without it the rename can reach the disk before the bytes do, and a
	// power loss in that window leaves a correctly named file full of zeros.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// The rename lives in the DIRECTORY, and syncing the file does not sync the entry that names
	// it. Without this the bytes are on the disk under a name that is not, so an apply that
	// writes several files can come back after a power loss as a mixture of old and new — which
	// is the one outcome the atomic write exists to prevent.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
