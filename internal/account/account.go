// Package account is the single source of truth: what an account IS, what is derived from it, and
// the registry that holds them.
//
// Everything mailforge writes is a pure function of this plus the program config. The generated
// files are OUTPUTS and never inputs — parsing them back would recreate the drift problem in the
// read direction and turn every format change into a migration of a parser. accounts.json is the
// database; the rest is its shadow.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/lvim-tech/mailforge/internal/config"
)

// Security is how a connection is protected.
type Security string

const (
	// TLS is TLS from the first byte — IMAPS on 993, SMTPS on 465.
	TLS Security = "tls"
	// STARTTLS is a plaintext connection upgraded before authentication, on 143 or 587.
	STARTTLS Security = "starttls"
)

// Server is one endpoint of an account.
//
// IMAP and SMTP are separate values rather than a shared host with two ports, because they are
// genuinely different machines often enough: this user's own accounts read from
// siera.superhosting.bg and send through credo.superhosting.bg.
type Server struct {
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Security  Security `json:"security"`
	LoginUser string   `json:"login_user,omitempty"` // empty means the address itself
}

// User is the name to log in with.
func (s Server) User(address string) string {
	if strings.TrimSpace(s.LoginUser) != "" {
		return s.LoginUser
	}
	return address
}

// Folders are the server's own names for the special mailboxes.
//
// DISCOVERED, NOT GUESSED. The verification session asks the server with LIST ... SPECIAL-USE, so
// Gmail answers "[Gmail]/Sent Mail" and a cPanel host answers "Sent", from the server rather than
// from a table that will be wrong for the next provider. The old hand-made setup hardcoded both
// spellings by hand, in different files, and that is exactly the drift this kills.
type Folders struct {
	// Delim is the server's hierarchy separator, learned from the same LIST reply the names
	// come from. Kept because a server name is NOT the local name: mbsync stores the mailbox
	// the server calls "INBOX.Sent" in a directory called "Sent", and NeoMutt reads the
	// directory. Without the delimiter that translation cannot be made, and the first attempt
	// at this wrote the server's spelling into NeoMutt's configuration — where it named
	// nothing — while mbsync refused the pre-created directories outright, because a local
	// name containing the server's delimiter is ambiguous.
	Delim   string `json:"delim,omitempty"`
	Sent    string `json:"sent,omitempty"`
	Drafts  string `json:"drafts,omitempty"`
	Trash   string `json:"trash,omitempty"`
	Archive string `json:"archive,omitempty"`
	Spam    string `json:"spam,omitempty"`
}

// Any reports whether the server named any special mailbox at all. A discovery that came back
// empty — an old server without SPECIAL-USE and without recognisable English names — must not be
// allowed to erase names that are already recorded.
func (f Folders) Any() bool {
	return f.Sent != "" || f.Drafts != "" || f.Trash != "" || f.Archive != "" || f.Spam != ""
}

// Quirks are the things a provider does differently, recorded per account because they are facts
// about that account's server and not about mailforge.
type Quirks struct {
	// NoRecord says the server files sent mail itself, so NeoMutt must not also save a copy.
	// Gmail does this; saving anyway gives every sent message twice.
	NoRecord bool `json:"no_record,omitempty"`
	// SyncExclude are mailboxes mbsync must not fetch — Gmail's All Mail is every message a
	// second time, which doubles a maildir for nothing.
	SyncExclude []string `json:"sync_exclude,omitempty"`
}

// Account is one mailbox, as stored.
type Account struct {
	Address  string  `json:"address"`
	RealName string  `json:"real_name,omitempty"`
	Label    string  `json:"label,omitempty"`
	IMAP     Server  `json:"imap"`
	SMTP     Server  `json:"smtp"`
	Auth     string  `json:"auth,omitempty"` // "password"; "oauth2" is the reserved seam
	Folders  Folders `json:"folders"`
	Quirks   Quirks  `json:"quirks,omitempty"`
	// Signature is the text NeoMutt appends to everything sent from this account. Kept HERE
	// rather than only as a file, because the registry is what the doctor compares against —
	// a signature that lived only on disk would be the one artefact nothing could tell had
	// been edited, emptied or lost.
	Signature string    `json:"signature,omitempty"`
	Order     int       `json:"order"`
	Default   bool      `json:"default,omitempty"`
	Verified  time.Time `json:"verified,omitempty"` // zero means never
}

// SigFile is where the signature is written for NeoMutt to read. Owned whole by mailforge, like
// the account rc beside it.
func (a Account) SigFile(c config.Config) string {
	return filepath.Join(c.NeomuttDir, "mailforge", "signatures", a.Address+".sig")
}

// Domain is the part after the @, lowercased.
func (a Account) Domain() string {
	if i := strings.LastIndex(a.Address, "@"); i >= 0 {
		return strings.ToLower(a.Address[i+1:])
	}
	return ""
}

// Local is the part before the @.
func (a Account) Local() string {
	if i := strings.LastIndex(a.Address, "@"); i >= 0 {
		return a.Address[:i]
	}
	return a.Address
}

// DefaultLabel is the sidebar header derived from the address: "EXAMPLE.COM | HELLO".
//
// Derived rather than required, because a label is the kind of field that gets left empty and then
// shows as a blank line in the sidebar — and a default that reads well is the difference between a
// field people fill in and a field people skip.
func (a Account) DefaultLabel() string {
	d, l := a.Domain(), a.Local()
	if d == "" {
		return strings.ToUpper(a.Address)
	}
	return strings.ToUpper(d) + " | " + strings.ToUpper(l)
}

// LabelOrDefault is what the sidebar shows.
func (a Account) LabelOrDefault() string {
	if strings.TrimSpace(a.Label) != "" {
		return a.Label
	}
	return a.DefaultLabel()
}

// LocalFolder translates a server mailbox name into the directory mbsync keeps it in.
//
// Two rules, both taken from what mbsync actually did on this machine rather than from reading
// about it: the INBOX namespace prefix is dropped, and the remaining delimiters become slashes.
// So "INBOX.Sent" with delimiter "." becomes "Sent", and Gmail's "[Gmail]/Sent Mail" with
// delimiter "/" is already in that form and passes through unchanged.
func (a Account) LocalFolder(server string) string {
	if server == "" {
		return ""
	}
	d := a.Folders.Delim
	if d == "" || d == "NIL" {
		return server
	}
	if strings.HasPrefix(server, "INBOX"+d) {
		server = strings.TrimPrefix(server, "INBOX"+d)
	}
	if d != "/" {
		server = strings.ReplaceAll(server, d, "/")
	}
	return server
}

// LocalFolders is the whole set, translated.
func (a Account) LocalFolders() Folders {
	return Folders{
		Delim:   a.Folders.Delim,
		Sent:    a.LocalFolder(a.Folders.Sent),
		Drafts:  a.LocalFolder(a.Folders.Drafts),
		Trash:   a.LocalFolder(a.Folders.Trash),
		Archive: a.LocalFolder(a.Folders.Archive),
		Spam:    a.LocalFolder(a.Folders.Spam),
	}
}

// PassEntry is where this account's password lives.
func (a Account) PassEntry(c config.Config) string {
	return path(c.PassPrefix, a.Address)
}

// Maildir is where mbsync writes and NeoMutt reads.
func (a Account) Maildir(c config.Config) string {
	return filepath.Join(c.MaildirRoot, a.Address)
}

// RCFile is the per-account neomutt file mailforge owns whole.
func (a Account) RCFile(c config.Config) string {
	return filepath.Join(c.NeomuttDir, "mailforge", "accounts", a.Address+".rc")
}

// Channel, FarStore and NearStore are the names mbsync knows this account by.
func (a Account) Channel() string   { return a.Address }
func (a Account) FarStore() string  { return a.Address + "-remote" }
func (a Account) NearStore() string { return a.Address + "-local" }

// SMTPURL is what NeoMutt is told to send through.
//
// The login is URL-ENCODED, and that is not decoration: the login is nearly always an address, so
// it contains an @, and an unencoded one turns "hello@example.com@credo…" into a host of
// "example.com@credo…". It is the single most likely mistake in a hand-written smtp_url and
// it fails at send time with a DNS error that names a host nobody typed.
func (a Account) SMTPURL() string {
	scheme := "smtps"
	if a.SMTP.Security == STARTTLS {
		scheme = "smtp"
	}
	return fmt.Sprintf("%s://%s@%s:%d/", scheme, url.QueryEscape(a.SMTP.User(a.Address)), a.SMTP.Host, a.SMTP.Port)
}

// path joins store path elements with forward slashes, which is what pass uses on every platform.
func path(parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.Trim(strings.TrimSpace(p), "/"); p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, "/")
}

// Validate refuses an account that cannot work, before anything is written for it.
func (a Account) Validate() error {
	if strings.TrimSpace(a.Address) == "" {
		return errors.New("an address is required")
	}
	if !strings.Contains(a.Address, "@") || a.Domain() == "" || a.Local() == "" {
		return fmt.Errorf("%q is not an address", a.Address)
	}
	// The address becomes a filename, a maildir name and a pass entry. A separator or a
	// leading dot in it would put those somewhere nobody asked for.
	if strings.ContainsAny(a.Address, "/\\") || strings.HasPrefix(a.Address, ".") {
		return fmt.Errorf("%q cannot be used as a path element", a.Address)
	}
	// And it becomes part of a SHELL COMMAND LINE — `pass show <prefix>/<address>` in the
	// generated neomuttrc and .mbsyncrc. Quoting alone would be enough for the shell, and the
	// renderer does quote, but an address is also written into a folder-hook pattern and a
	// systemd-adjacent path, so the honest place to stop the odd ones is here, where the answer
	// can name the character. Everything a real mailbox needs is allowed.
	for _, r := range a.Address {
		if badAddressRune(r) {
			return fmt.Errorf("%q contains a character an address cannot have: %q", a.Address, string(r))
		}
	}
	for _, s := range []struct {
		what string
		srv  Server
	}{{"IMAP", a.IMAP}, {"SMTP", a.SMTP}} {
		if strings.TrimSpace(s.srv.Host) == "" {
			return fmt.Errorf("the %s host is required", s.what)
		}
		if s.srv.Port <= 0 || s.srv.Port > 65535 {
			return fmt.Errorf("the %s port is out of range: %d", s.what, s.srv.Port)
		}
		if s.srv.Security != TLS && s.srv.Security != STARTTLS {
			return fmt.Errorf("the %s security must be tls or starttls, not %q", s.what, s.srv.Security)
		}
	}
	return nil
}

// badAddressRune names the characters an address must not carry: control characters, whitespace,
// and the shell and regex metacharacters that would change the meaning of a generated command line
// or of a folder-hook pattern. Letters and digits of any script pass, so an internationalised
// mailbox is still accepted; what is refused is the punctuation no real mailbox uses and every
// injection does.
func badAddressRune(r rune) bool {
	if r < 0x20 || r == 0x7f || unicode.IsSpace(r) {
		return true
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	return !strings.ContainsRune(".@_+-=~", r)
}

// Registry is the account list as stored.
type Registry struct {
	Accounts []Account `json:"accounts"`
}

// Clone returns a registry that shares nothing with this one.
//
// Copying a Registry by assignment copies the SLICE HEADER, not the accounts: both copies keep
// pointing at one backing array, so Upsert, Remove and the in-place sort in normalise write into
// memory the other copy is reading. The interface hands a registry to a background apply and goes
// on drawing the list from its own, which is exactly that race — a deep copy at the hand-off is
// what keeps the two apart.
func (r Registry) Clone() Registry {
	return Registry{Accounts: append([]Account(nil), r.Accounts...)}
}

// RegistryPath sits next to config.json: accounts.json is configuration too, and a user may
// legitimately hand-edit it — mailforge re-renders from it, so an edit there is safe by design,
// unlike an edit to a rendered file.
func RegistryPath() string {
	return filepath.Join(filepath.Dir(config.Path()), "accounts.json")
}

// Load reads the registry. An absent file is an empty registry, not an error: that is what a
// machine looks like before its first account.
func Load() (Registry, error) {
	var r Registry
	b, err := os.ReadFile(RegistryPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return r, nil
		}
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Registry{}, fmt.Errorf("%s: %w", RegistryPath(), err)
	}
	r.normalise()
	return r, nil
}

// Save writes the registry atomically, after putting it in order.
func (r *Registry) Save() error {
	r.normalise()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	p := RegistryPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return config.WriteAtomic(p, append(b, '\n'), 0o600)
}

// normalise enforces the invariants the rest of the program is allowed to assume: stable order,
// dense Order values, and exactly one default when there is at least one account.
//
// Done on load as well as save, because a hand-edited file is expected and "the user wrote two
// defaults" must not become "two accounts claim the spoolfile" three layers away.
func (r *Registry) normalise() {
	sort.SliceStable(r.Accounts, func(i, j int) bool {
		if r.Accounts[i].Order != r.Accounts[j].Order {
			return r.Accounts[i].Order < r.Accounts[j].Order
		}
		return r.Accounts[i].Address < r.Accounts[j].Address
	})
	seenDefault := false
	for i := range r.Accounts {
		r.Accounts[i].Order = i
		if r.Accounts[i].Default {
			if seenDefault {
				r.Accounts[i].Default = false
				continue
			}
			seenDefault = true
		}
	}
	if !seenDefault && len(r.Accounts) > 0 {
		r.Accounts[0].Default = true
	}
}

// Find returns the account with this address.
func (r Registry) Find(address string) (Account, bool) {
	for _, a := range r.Accounts {
		if strings.EqualFold(a.Address, address) {
			return a, true
		}
	}
	return Account{}, false
}

// Default returns the account that provides the startup identity and spoolfile.
func (r Registry) Default() (Account, bool) {
	for _, a := range r.Accounts {
		if a.Default {
			return a, true
		}
	}
	if len(r.Accounts) > 0 {
		return r.Accounts[0], true
	}
	return Account{}, false
}

// Upsert adds an account or replaces the one with the same address.
func (r *Registry) Upsert(a Account) error {
	if err := a.Validate(); err != nil {
		return err
	}
	for i := range r.Accounts {
		if strings.EqualFold(r.Accounts[i].Address, a.Address) {
			a.Order = r.Accounts[i].Order
			r.Accounts[i] = a
			r.normalise()
			return nil
		}
	}
	a.Order = len(r.Accounts)
	r.Accounts = append(r.Accounts, a)
	r.normalise()
	return nil
}

// Remove drops an account from the registry. What happens to its files, its mail and its password
// is the caller's decision and deliberately not this function's — deleting a record and deleting
// somebody's mail are different acts and must not share a code path.
func (r *Registry) Remove(address string) bool {
	for i := range r.Accounts {
		if strings.EqualFold(r.Accounts[i].Address, address) {
			r.Accounts = append(r.Accounts[:i], r.Accounts[i+1:]...)
			r.normalise()
			return true
		}
	}
	return false
}

// SetDefault makes one account the default and clears the rest.
func (r *Registry) SetDefault(address string) bool {
	found := false
	for i := range r.Accounts {
		is := strings.EqualFold(r.Accounts[i].Address, address)
		r.Accounts[i].Default = is
		found = found || is
	}
	if !found {
		r.normalise()
	}
	return found
}

// Move shifts an account up or down the sidebar order by one place.
func (r *Registry) Move(address string, delta int) bool {
	for i := range r.Accounts {
		if !strings.EqualFold(r.Accounts[i].Address, address) {
			continue
		}
		j := i + delta
		if j < 0 || j >= len(r.Accounts) {
			return false
		}
		// The Order values move WITH the elements. Swapping only positions leaves both
		// carrying their old Order, and normalise sorts by exactly that — so the list comes
		// back in the order it started and the move silently does nothing.
		r.Accounts[i], r.Accounts[j] = r.Accounts[j], r.Accounts[i]
		r.Accounts[i].Order, r.Accounts[j].Order = i, j
		r.normalise()
		return true
	}
	return false
}
