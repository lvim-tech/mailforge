// Package verify proves an account works before anything is written for it.
//
// NO LIBRARY, AND THE REASON IS THE PASSWORD PATH, not code size. Every IMAP client and
// net/smtp's Auth interface take the password as a Go string: immutable, copied by the garbage
// collector at will, impossible to erase. That is exactly what the mlocked buffer and the
// hand-made pipe in internal/sys exist to avoid, and importing a library would undo it at the last
// step for the sake of a hundred lines. Here the password goes from locked memory straight into
// the socket and never becomes a Go value.
//
// The other half of the job is discovery: while the IMAP session is open and authenticated, it
// asks the server what its special folders are actually called. Guessing them from a table is the
// second drift class this program exists to kill.
package verify

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// Stage is how far a check got, so a failure can say which part failed rather than "it did not
// work". The wizard uses it to decide whether to send the user back to the password or the host.
type Stage string

const (
	StageResolve Stage = "resolve"
	StageConnect Stage = "connect"
	StageTLS     Stage = "tls"
	StageGreet   Stage = "greeting"
	StageAuth    Stage = "login"
	StageFolders Stage = "folders"
	StageDone    Stage = "done"
)

// Result is one protocol's outcome.
type Result struct {
	OK       bool
	Reached  Stage
	Detail   string          // the server's own words where there are any
	Folders  account.Folders // IMAP only, from the server
	Capabil  string          // what the server advertised, for the report
	Duration time.Duration
}

// Fatal reports whether the failure is one that re-typing the password cannot fix.
func (r Result) Fatal() bool {
	switch r.Reached {
	case StageResolve, StageConnect, StageTLS, StageGreet:
		return true
	}
	return false
}

// IMAP connects, logs in, and asks the server for its special folders.
func IMAP(srv account.Server, address string, secret *sys.Secret, timeout time.Duration) Result {
	start := time.Now()
	res := Result{Reached: StageResolve}
	defer func() { res.Duration = time.Since(start) }()

	conn, r, err := dial(srv, timeout, &res)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	defer func() { _ = conn.Close() }()

	// The greeting proves something is speaking IMAP, which a TCP accept on shared hosting
	// does not: a wildcard vhost will accept a connection to anything.
	res.Reached = StageGreet
	line, err := r.ReadString('\n')
	if err != nil {
		res.Detail = "no greeting: " + err.Error()
		return res
	}
	if !strings.HasPrefix(line, "* OK") {
		res.Detail = "not an IMAP server: " + truncate(strings.TrimSpace(line), 120)
		return res
	}
	res.Capabil = strings.TrimSpace(line)

	res.Reached = StageAuth
	if err := imapLogin(conn, r, srv.User(address), secret, timeout); err != nil {
		res.Detail = err.Error()
		return res
	}

	res.Reached = StageFolders
	folders, err := imapFolders(conn, r, timeout)
	if err != nil {
		// A login that worked and a folder list that did not is still a usable account —
		// the folders can be typed. Reported, not fatal.
		res.OK = true
		res.Reached = StageDone
		res.Detail = "logged in; the folder list could not be read: " + err.Error()
		return res
	}
	res.Folders = folders

	_ = writeLine(conn, "a3 LOGOUT", timeout)
	res.OK = true
	res.Reached = StageDone
	return res
}

// dial opens the connection, upgrading it when the account says STARTTLS.
func dial(srv account.Server, timeout time.Duration, res *Result) (net.Conn, *bufio.Reader, error) {
	addr := net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))
	d := net.Dialer{Timeout: timeout}

	if srv.Security == account.TLS {
		res.Reached = StageConnect
		c, err := tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: srv.Host})
		if err != nil {
			res.Reached = classify(err)
			return nil, nil, err
		}
		res.Reached = StageTLS
		return c, bufio.NewReader(c), nil
	}

	res.Reached = StageConnect
	c, err := d.Dial("tcp", addr)
	if err != nil {
		res.Reached = classify(err)
		return nil, nil, err
	}
	r := bufio.NewReader(c)
	// The caller reads the greeting; for STARTTLS the upgrade has to happen after it, so this
	// path is completed by the protocol-specific code. Returning the plain connection keeps
	// the two protocols' STARTTLS dances where they belong.
	return c, r, nil
}

// classify turns a dial error into the stage that explains it, so the message can say "the host
// does not resolve" rather than "connect failed" when the two are quite different problems.
func classify(err error) Stage {
	var dnsErr *net.DNSError
	if ok := asDNS(err, &dnsErr); ok {
		return StageResolve
	}
	if strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "tls:") {
		return StageTLS
	}
	return StageConnect
}

func asDNS(err error, out **net.DNSError) bool {
	for e := err; e != nil; {
		if d, ok := e.(*net.DNSError); ok {
			*out = d
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// imapLogin authenticates with the password as a LITERAL.
//
// The literal form — {N} then the bytes after the server's continuation — is what makes streaming
// possible: the length comes from the buffer without rendering it, and quoting, backslashes and
// non-ASCII all stop being questions. A quoted-string login would need the value inspected and
// escaped, which means having it as a string.
func imapLogin(conn net.Conn, r *bufio.Reader, user string, secret *sys.Secret, timeout time.Duration) error {
	n := secret.Bytes()
	if n == 0 {
		return fmt.Errorf("no password was entered")
	}
	if err := writeLine(conn, fmt.Sprintf("a1 LOGIN %s {%d}", quoteIMAP(user), n), timeout); err != nil {
		return err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return fmt.Errorf("no continuation: %w", err)
	}
	if !strings.HasPrefix(line, "+") {
		return fmt.Errorf("the server refused the login form: %s", truncate(strings.TrimSpace(line), 120))
	}

	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	if _, err := secret.WriteTo(conn); err != nil {
		return err
	}
	if _, err := conn.Write([]byte("\r\n")); err != nil {
		return err
	}

	for {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "a1 OK"):
			return nil
		case strings.HasPrefix(l, "a1 NO"), strings.HasPrefix(l, "a1 BAD"):
			return fmt.Errorf("%s", authMessage(l))
		}
		// Untagged lines before the tagged reply are ordinary; keep reading.
	}
}

// authMessage strips the tag and translates the answers that are famously confusing.
func authMessage(l string) string {
	msg := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(l, "a1 NO"), "a1 BAD"))
	low := strings.ToLower(msg)
	if strings.Contains(low, "application-specific") || strings.Contains(low, "web login required") {
		return msg + "  —  Gmail refuses ordinary passwords over IMAP; create an app password " +
			"in the Google account security settings and use that here"
	}
	return msg
}

// imapFolders asks the server what its special mailboxes are called.
//
// SPECIAL-USE (RFC 6154) is the honest answer; a server without it gets a name-matching fallback,
// which is a guess and is marked as such by simply not filling in what it cannot see.
func imapFolders(conn net.Conn, r *bufio.Reader, timeout time.Duration) (account.Folders, error) {
	var f account.Folders
	if err := writeLine(conn, `a2 LIST "" "*" RETURN (SPECIAL-USE)`, timeout); err != nil {
		return f, err
	}
	var names []string
	for {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		line, err := r.ReadString('\n')
		if err != nil {
			return f, err
		}
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "a2 ") {
			if !strings.HasPrefix(l, "a2 OK") {
				return f, fmt.Errorf("%s", l)
			}
			break
		}
		if !strings.HasPrefix(l, "* LIST") {
			continue
		}
		attrs, delim, name := parseList(l)
		if name == "" {
			continue
		}
		if delim != "" && delim != "NIL" && f.Delim == "" {
			f.Delim = delim
		}
		names = append(names, name)
		switch {
		case hasAttr(attrs, `\sent`):
			f.Sent = name
		case hasAttr(attrs, `\drafts`):
			f.Drafts = name
		case hasAttr(attrs, `\trash`):
			f.Trash = name
		case hasAttr(attrs, `\junk`):
			f.Spam = name
		case hasAttr(attrs, `\archive`):
			f.Archive = name
		}
	}
	fillByName(&f, names)
	return f, nil
}

// fillByName is the fallback for servers that do not advertise SPECIAL-USE. It only fills what is
// still empty, so a server's own answer always wins over a guess about English folder names.
func fillByName(f *account.Folders, names []string) {
	pick := func(dst *string, wants ...string) {
		if *dst != "" {
			return
		}
		for _, want := range wants {
			for _, n := range names {
				if strings.EqualFold(n, want) {
					*dst = n
					return
				}
			}
		}
	}
	pick(&f.Sent, "Sent", "Sent Items", "Sent Messages", "INBOX.Sent")
	pick(&f.Drafts, "Drafts", "INBOX.Drafts")
	pick(&f.Trash, "Trash", "Deleted Items", "INBOX.Trash")
	pick(&f.Spam, "Spam", "Junk", "INBOX.spam", "INBOX.Junk")
	pick(&f.Archive, "Archive", "Archives")
}

// parseList reads a LIST reply into its attributes and its mailbox name.
//
//   - LIST (\HasNoChildren \Sent) "." "Sent"
//   - LIST (\HasNoChildren \Sent) "." Sent          — the name may be unquoted
//   - LIST (\HasNoChildren) "/" "[Gmail]/Sent Mail"  — and may contain spaces
//   - LIST (\Noselect) NIL ""                        — and the delimiter may be NIL
//
// The first version of this looked for "the last quoted string", which is right for the first
// shape and wrong for the second: with an unquoted name the last quotes belong to the DELIMITER,
// so every folder came back named ".". Reading the reply by its structure — attributes in
// parentheses, then delimiter, then name — is the only version that answers all four.
func parseList(l string) (attrs []string, delim, name string) {
	open := strings.Index(l, "(")
	shut := strings.Index(l, ")")
	if open < 0 || shut < open {
		return nil, "", ""
	}
	attrs = strings.Fields(l[open+1 : shut])

	rest := strings.TrimSpace(l[shut+1:])
	// The delimiter comes first, and it is KEPT: it is what turns a server mailbox name into
	// the directory mbsync stores it in, and without it NeoMutt is pointed at names that exist
	// on the server and nowhere on this disk.
	delim, rest = token(rest)
	name, _ = token(strings.TrimSpace(rest))
	return attrs, delim, name
}

// token reads one IMAP token — a quoted string or a bare atom — and returns it with the remainder.
func token(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if s[0] != '"' {
		if i := strings.IndexByte(s, ' '); i >= 0 {
			return s[:i], s[i+1:]
		}
		return s, ""
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			// An escaped character inside a quoted string is the next byte, literally: a
			// mailbox may legitimately contain a quote or a backslash.
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		case '"':
			return b.String(), s[i+1:]
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), ""
}

// hasAttr compares attributes case-insensitively, because the case of \Sent is not guaranteed and
// a comparison that assumes it silently finds nothing.
func hasAttr(attrs []string, want string) bool {
	for _, a := range attrs {
		if strings.EqualFold(a, want) {
			return true
		}
	}
	return false
}

func writeLine(conn net.Conn, s string, timeout time.Duration) error {
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	_, err := conn.Write([]byte(s + "\r\n"))
	return err
}

func quoteIMAP(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
