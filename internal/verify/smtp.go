package verify

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// SMTP proves the account can SEND, and deliberately sends nothing.
//
// Authentication is the whole question. A test message to yourself is a side effect nobody asked
// for, arrives in a mailbox somebody has to clean, and proves no more than the 235 reply does.
func SMTP(srv account.Server, address string, secret *sys.Secret, timeout time.Duration) Result {
	start := time.Now()
	res := Result{Reached: StageResolve}
	defer func() { res.Duration = time.Since(start) }()

	conn, r, err := dial(srv, timeout, &res)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	defer func() { _ = conn.Close() }()

	res.Reached = StageGreet
	code, line, err := smtpReply(conn, r, timeout)
	if err != nil {
		res.Detail = "no greeting: " + err.Error()
		return res
	}
	if code != 220 {
		res.Detail = "not an SMTP server: " + truncate(line, 120)
		return res
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "localhost"
	}
	caps, err := smtpEHLO(conn, r, host, timeout)
	if err != nil {
		res.Detail = err.Error()
		return res
	}

	// STARTTLS when the account says so: the password must not cross a plaintext connection,
	// and a server that will not upgrade is a server this program will not authenticate to.
	if srv.Security == account.STARTTLS {
		res.Reached = StageTLS
		if !strings.Contains(strings.ToUpper(caps), "STARTTLS") {
			res.Detail = "the server does not offer STARTTLS, so the password would cross in the clear"
			return res
		}
		if err := writeLine(conn, "STARTTLS", timeout); err != nil {
			res.Detail = err.Error()
			return res
		}
		if code, line, err := smtpReply(conn, r, timeout); err != nil || code != 220 {
			res.Detail = "STARTTLS refused: " + truncate(line, 120)
			return res
		}
		tc := tls.Client(conn, &tls.Config{ServerName: srv.Host})
		if err := tc.Handshake(); err != nil {
			res.Detail = err.Error()
			return res
		}
		conn, r = tc, bufio.NewReader(tc)
		// EHLO again: the capability list after the upgrade is the one that counts, and AUTH
		// is routinely absent before it.
		if caps, err = smtpEHLO(conn, r, host, timeout); err != nil {
			res.Detail = err.Error()
			return res
		}
	}
	res.Capabil = strings.TrimSpace(caps)

	res.Reached = StageAuth
	if err := smtpAuth(conn, r, srv.User(address), secret, caps, timeout); err != nil {
		res.Detail = err.Error()
		return res
	}
	_ = writeLine(conn, "QUIT", timeout)

	res.OK = true
	res.Reached = StageDone
	return res
}

// smtpAuth authenticates with the password STREAMED, never assembled.
//
// AUTH PLAIN's argument is base64 of "\0user\0password". Building that string would put the
// password in the heap, so instead the encoder is wrapped around the connection and the parts are
// written through it: the only place the whole credential exists is the kernel's socket buffer.
func smtpAuth(conn net.Conn, r *bufio.Reader, user string, secret *sys.Secret, caps string, timeout time.Duration) error {
	up := strings.ToUpper(caps)
	switch {
	case strings.Contains(up, "PLAIN"):
		_ = conn.SetWriteDeadline(time.Now().Add(timeout))
		if _, err := conn.Write([]byte("AUTH PLAIN ")); err != nil {
			return err
		}
		enc := base64.NewEncoder(base64.StdEncoding, conn)
		if _, err := enc.Write([]byte("\x00" + user + "\x00")); err != nil {
			return err
		}
		if _, err := secret.WriteTo(enc); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		if _, err := conn.Write([]byte("\r\n")); err != nil {
			return err
		}

	case strings.Contains(up, "LOGIN"):
		// Two rounds, same discipline: the server asks for each part separately.
		if err := writeLine(conn, "AUTH LOGIN", timeout); err != nil {
			return err
		}
		if code, line, err := smtpReply(conn, r, timeout); err != nil || code != 334 {
			return fmt.Errorf("AUTH LOGIN refused: %s", truncate(line, 120))
		}
		if err := writeLine(conn, base64.StdEncoding.EncodeToString([]byte(user)), timeout); err != nil {
			return err
		}
		if code, line, err := smtpReply(conn, r, timeout); err != nil || code != 334 {
			return fmt.Errorf("the server rejected the user name: %s", truncate(line, 120))
		}
		enc := base64.NewEncoder(base64.StdEncoding, conn)
		if _, err := secret.WriteTo(enc); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		if _, err := conn.Write([]byte("\r\n")); err != nil {
			return err
		}

	default:
		return fmt.Errorf("the server advertises no authentication this program can use: %s",
			truncate(strings.TrimSpace(caps), 160))
	}

	code, line, err := smtpReply(conn, r, timeout)
	if err != nil {
		return err
	}
	if code != 235 {
		return fmt.Errorf("%s", smtpAuthMessage(line))
	}
	return nil
}

// smtpAuthMessage translates the rejections that send people looking in the wrong place.
func smtpAuthMessage(line string) string {
	low := strings.ToLower(line)
	if strings.Contains(low, "application-specific") || strings.Contains(low, "accounts.google.com") {
		return line + "  —  Gmail refuses ordinary passwords for SMTP; create an app password in " +
			"the Google account security settings and use that here"
	}
	return line
}

// smtpEHLO greets and collects the capability lines.
func smtpEHLO(conn net.Conn, r *bufio.Reader, host string, timeout time.Duration) (string, error) {
	if err := writeLine(conn, "EHLO "+host, timeout); err != nil {
		return "", err
	}
	code, caps, err := smtpReply(conn, r, timeout)
	if err != nil {
		return "", err
	}
	if code != 250 {
		return "", fmt.Errorf("EHLO refused: %s", truncate(caps, 120))
	}
	return caps, nil
}

// smtpReply reads one reply, including a multi-line one, and returns its code and text.
func smtpReply(conn net.Conn, r *bufio.Reader, timeout time.Duration) (int, string, error) {
	var (
		code int
		all  []string
	)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		line, err := r.ReadString('\n')
		if err != nil {
			return 0, strings.Join(all, " "), err
		}
		l := strings.TrimRight(line, "\r\n")
		if len(l) < 4 {
			all = append(all, l)
			return code, strings.Join(all, " "), fmt.Errorf("short reply: %q", l)
		}
		if c := atoi(l[:3]); c > 0 {
			code = c
		}
		all = append(all, strings.TrimSpace(l[4:]))
		// A dash in the fourth column means another line follows; a space means this was the
		// last. Getting this wrong is how a client hangs waiting for a reply already read.
		if l[3] != '-' {
			return code, strings.Join(all, " "), nil
		}
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
