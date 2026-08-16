package verify

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lvim-tech/mailforge/internal/account"
	"github.com/lvim-tech/mailforge/internal/sys"
)

// The bug these tests exist for: IMAP() read the greeting and went straight to LOGIN, whatever the
// account's security said. dial() returns a PLAIN connection for a STARTTLS account — the upgrade
// was documented as the protocol's own job, SMTP did it, and IMAP did not — so every account on
// port 143 sent its password over an unencrypted socket, from the same locked buffer the rest of
// the program goes to such lengths to protect.
//
// What is asserted is the property, not the implementation: the password bytes must never appear on
// a connection that was not upgraded first.

// fakeIMAP accepts one connection, plays the given script, and records every line it was sent.
func fakeIMAP(t *testing.T, greet bool, starttlsReply string) (addr string, lines func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	got := make(chan []string, 1)
	go func() {
		var seen []string
		defer func() { got <- seen }()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		if !greet {
			// A port that accepts and never speaks — a tarpit, or an IDS box. The client
			// must give up on its own rather than wait for a banner that never comes.
			time.Sleep(2 * time.Second)
			return
		}
		if _, err := c.Write([]byte("* OK IMAP4rev1 ready\r\n")); err != nil {
			return
		}
		r := bufio.NewReader(c)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			l := strings.TrimSpace(line)
			seen = append(seen, l)
			if strings.HasPrefix(l, "a0 STARTTLS") {
				if _, err := c.Write([]byte(starttlsReply + "\r\n")); err != nil {
					return
				}
				if strings.HasPrefix(starttlsReply, "a0 OK") {
					// This fake has no certificate, so it hangs up instead of
					// negotiating. The client must treat that as the end of the
					// check — never as a reason to carry on in the clear.
					return
				}
			}
		}
	}()

	var once []string
	return ln.Addr().String(), func() []string {
		if once == nil {
			once = <-got
		}
		return once
	}
}

func serverAt(t *testing.T, addr string, sec account.Security) account.Server {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	return account.Server{Host: host, Port: p, Security: sec}
}

func secretSaying(t *testing.T, s string) *sys.Secret {
	t.Helper()
	sec, err := sys.NewSecret(64)
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	t.Cleanup(sec.Close)
	sec.Set([]byte(s))
	return sec
}

// TestIMAPRefusesToLoginWithoutTheUpgrade is the regression itself: a server that will not upgrade
// must end the check, and the password must not have been sent.
func TestIMAPRefusesToLoginWithoutTheUpgrade(t *testing.T) {
	addr, lines := fakeIMAP(t, true, "a0 NO STARTTLS not available")
	res := IMAP(serverAt(t, addr, account.STARTTLS), "someone@example.com",
		secretSaying(t, "hunter2"), 2*time.Second)

	if res.OK {
		t.Fatal("the check succeeded against a server that refused to upgrade")
	}
	if res.Reached != StageTLS {
		t.Errorf("reached %q, want %q — the failure must name the upgrade", res.Reached, StageTLS)
	}
	sent := strings.Join(lines(), "\n")
	if !strings.Contains(sent, "a0 STARTTLS") {
		t.Errorf("the client never asked for STARTTLS; it sent:\n%s", sent)
	}
	if strings.Contains(sent, "LOGIN") || strings.Contains(sent, "hunter2") {
		t.Fatalf("the password path was entered on a plaintext connection:\n%s", sent)
	}
}

// TestIMAPSendsNoPasswordBeforeTheHandshake covers the other half: the server AGREES to upgrade,
// the TLS handshake then fails (this fake has no certificate) — and still nothing was sent in the
// clear. The check must fail, not fall back.
func TestIMAPSendsNoPasswordBeforeTheHandshake(t *testing.T) {
	addr, lines := fakeIMAP(t, true, "a0 OK begin TLS negotiation now")
	res := IMAP(serverAt(t, addr, account.STARTTLS), "someone@example.com",
		secretSaying(t, "hunter2"), 2*time.Second)

	if res.OK {
		t.Fatal("the check succeeded although the handshake could not complete")
	}
	sent := strings.Join(lines(), "\n")
	if strings.Contains(sent, "LOGIN") || strings.Contains(sent, "hunter2") {
		t.Fatalf("the password crossed the plaintext connection:\n%s", sent)
	}
}

// TestIMAPGivesUpOnASilentServer locks the read deadline on the greeting. Without it the goroutine
// blocked for ever: the wizard kept saying "checking …" and the message that clears it never came.
func TestIMAPGivesUpOnASilentServer(t *testing.T) {
	addr, _ := fakeIMAP(t, false, "")
	done := make(chan Result, 1)
	go func() {
		done <- IMAP(serverAt(t, addr, account.TLS), "someone@example.com",
			secretSaying(t, "hunter2"), 200*time.Millisecond)
	}()
	select {
	case res := <-done:
		if res.OK {
			t.Fatal("a server that said nothing was accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("IMAP() did not return — the greeting read has no deadline")
	}
}

// TestQuoteIMAPDropsControlCharacters: the user name can come from an autoconfig server, and a CR
// or LF in it would end the LOGIN line and inject a second command into this session.
func TestQuoteIMAPDropsControlCharacters(t *testing.T) {
	got := quoteIMAP("user\r\na9 EXAMINE INBOX")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("quoteIMAP kept a line break: %q", got)
	}
	if want := `"usera9 EXAMINE INBOX"`; got != want {
		t.Errorf("quoteIMAP = %q, want %q", got, want)
	}
}
