// Package provider works out where an address's servers are.
//
// Four sources, each tried only if the one before produced nothing, and the answer is ALWAYS shown
// in an editable form afterwards: discovery proposes, the user disposes. Nothing here is trusted
// until verification has authenticated against it.
//
// The order is by cost and by trust: the built-in table is instant and certain, DNS is nearly
// free, the ISPDB leaves the machine, and the probe guesses hostnames. Each says where it came
// from, because "imap.example.com, guessed" and "imap.example.com, the domain's own SRV record"
// deserve different amounts of confidence from the person reading the form.
package provider

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lvim-tech/mailforge/internal/account"
)

// Guess is what discovery came back with.
type Guess struct {
	IMAP   account.Server
	SMTP   account.Server
	Source string          // named in the form, so the reader knows how much to trust it
	Note   string          // provider-specific warnings, e.g. Gmail's app password
	Quirks account.Quirks  // what this provider does differently
	Folder account.Folders // only ever filled by verification, never by a guess
}

// Found reports whether anything usable came back.
func (g Guess) Found() bool { return g.IMAP.Host != "" || g.SMTP.Host != "" }

// entry is one row of the built-in table.
type entry struct {
	domains []string
	imap    account.Server
	smtp    account.Server
	note    string
	quirks  account.Quirks
}

// table is deliberately SMALL. It carries the providers whose behaviour needs quirks recorded, not
// a directory of the world's hosters — the ISPDB already is one, and a big table here would be a
// second copy of it, going stale in a different way.
//
// Shared-hosting domains cannot be in it at all: the server for an address at a custom domain is a
// per-hosting-account name (siera, credo) that no rule derives from the address.
var table = []entry{
	{
		domains: []string{"gmail.com", "googlemail.com"},
		imap:    account.Server{Host: "imap.gmail.com", Port: 993, Security: account.TLS},
		smtp:    account.Server{Host: "smtp.gmail.com", Port: 465, Security: account.TLS},
		note: "Gmail refuses ordinary account passwords over IMAP and SMTP. Create an app " +
			"password in the Google account security settings and enter that one here.",
		quirks: account.Quirks{
			// Gmail files sent mail itself; a second copy from the client duplicates it.
			NoRecord: true,
			// All Mail is every message a second time — fetching it doubles the maildir.
			SyncExclude: []string{"[Gmail]/All Mail"},
		},
	},
	{
		domains: []string{"outlook.com", "hotmail.com", "live.com"},
		imap:    account.Server{Host: "outlook.office365.com", Port: 993, Security: account.TLS},
		smtp:    account.Server{Host: "smtp.office365.com", Port: 587, Security: account.STARTTLS},
		note:    "Microsoft accounts with two-step verification need an app password.",
	},
	{
		domains: []string{"yahoo.com"},
		imap:    account.Server{Host: "imap.mail.yahoo.com", Port: 993, Security: account.TLS},
		smtp:    account.Server{Host: "smtp.mail.yahoo.com", Port: 465, Security: account.TLS},
		note:    "Yahoo requires an app password for mail clients.",
	},
}

// Discover runs the chain within one budget.
//
// The budget is the whole point of the context: offline, every network step fails fast and the
// user reaches the manual form in about a second, instead of watching a spinner for the sum of
// four timeouts.
func Discover(ctx context.Context, address string, allowISPDB bool) Guess {
	domain := domainOf(address)
	if domain == "" {
		return Guess{Source: "the address has no domain — enter the servers by hand"}
	}

	if g, ok := fromTable(domain); ok {
		return g
	}
	if g, ok := fromSRV(ctx, domain); ok {
		return g
	}
	if allowISPDB {
		if g, ok := fromISPDB(ctx, domain, address); ok {
			return g
		}
	}
	if g, ok := probe(ctx, domain); ok {
		return g
	}
	// LAST, because it answers a slightly different question than everything above.
	//
	// MX names the server that RECEIVES mail for the domain, which is not by definition the one a
	// client connects to — so it is a weaker signal than a conventional name that answered, and it
	// is consulted only when nothing better did. On shared hosting it is usually the whole answer:
	// the domain's own "mail." name points at the WEB host, which runs no IMAP at all, while MX
	// points at the hosting server that does. Nothing derivable from the address distinguishes
	// those two, which is why that case used to end in "nothing found" and a trip to a control
	// panel to read a hostname the DNS already knew.
	if g, ok := fromMX(ctx, domain); ok {
		return g
	}
	return Guess{Source: "nothing found — enter the servers by hand"}
}

// fromMX takes the domain's mail exchangers and asks each whether it also speaks to clients.
//
// It CONFIRMS rather than assumes: a host is only offered after it greets on the port, exactly as
// the conventional-name probe does. An MX that handles delivery but refuses client connections —
// a spam filter in front of the real server is the common one — therefore fills in nothing rather
// than sending the user at a machine that will not talk to them.
func fromMX(ctx context.Context, domain string) (Guess, bool) {
	var g Guess
	res := net.Resolver{}
	recs, err := res.LookupMX(ctx, domain)
	if err != nil || len(recs) == 0 {
		return g, false
	}
	g = greetAny(ctx, mxHosts(recs, domain), mxHosts(recs, domain))
	if !g.Found() {
		return g, false
	}
	g.Source = "found through the domain's MX record"
	g.Note = "This domain's mail is handled by another host, so the servers are named after the " +
		"hosting account rather than after your domain. That is also the name on the " +
		"certificate — using your own domain here would be refused."
	return g, true
}

// mxHosts turns MX records into candidate hostnames, most preferred first.
func mxHosts(recs []*net.MX, domain string) []string {
	// Sorted by preference already in practice, but the resolver does not promise it; the lowest
	// number is the primary.
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Pref < recs[j].Pref })

	var out []string
	seen := map[string]bool{}
	for _, mx := range recs {
		host := strings.TrimSuffix(mx.Host, ".")
		// The bare domain was already tried by the conventional-name probe, and a duplicate MX
		// target costs another full dial timeout to learn the same thing twice.
		if host == "" || strings.EqualFold(host, domain) || seen[strings.ToLower(host)] {
			continue
		}
		seen[strings.ToLower(host)] = true
		out = append(out, host)
	}
	return out
}

func domainOf(address string) string {
	if i := strings.LastIndex(address, "@"); i >= 0 {
		return strings.ToLower(strings.TrimSpace(address[i+1:]))
	}
	return ""
}

func fromTable(domain string) (Guess, bool) {
	for _, e := range table {
		for _, d := range e.domains {
			if d == domain {
				return Guess{
					IMAP: e.imap, SMTP: e.smtp,
					Source: "built in", Note: e.note, Quirks: e.quirks,
				}, true
			}
		}
	}
	return Guess{}, false
}

// fromSRV asks the domain itself, per RFC 6186.
//
// A record whose target is "." means the service is EXPLICITLY not offered — the standard's way of
// saying "do not go looking" — and is honoured rather than treated as an empty answer.
func fromSRV(ctx context.Context, domain string) (Guess, bool) {
	var g Guess
	res := net.Resolver{}

	if host, port, ok := srv(ctx, &res, "imaps", domain); ok {
		g.IMAP = account.Server{Host: host, Port: port, Security: account.TLS}
	}
	if g.IMAP.Host == "" {
		if host, port, ok := srv(ctx, &res, "imap", domain); ok {
			g.IMAP = account.Server{Host: host, Port: port, Security: account.STARTTLS}
		}
	}
	if host, port, ok := srv(ctx, &res, "submissions", domain); ok {
		g.SMTP = account.Server{Host: host, Port: port, Security: account.TLS}
	}
	if g.SMTP.Host == "" {
		if host, port, ok := srv(ctx, &res, "submission", domain); ok {
			g.SMTP = account.Server{Host: host, Port: port, Security: account.STARTTLS}
		}
	}
	if !g.Found() {
		return g, false
	}
	g.Source = "the domain's own SRV records"
	return g, true
}

func srv(ctx context.Context, res *net.Resolver, service, domain string) (string, int, bool) {
	_, recs, err := res.LookupSRV(ctx, service, "tcp", domain)
	if err != nil || len(recs) == 0 {
		return "", 0, false
	}
	r := recs[0]
	host := strings.TrimSuffix(r.Target, ".")
	if host == "" {
		// Target "." — the domain says this service is not offered here.
		return "", 0, false
	}
	return host, int(r.Port), true
}

// ispdbConfig is the slice of Thunderbird's autoconfig format that matters here.
type ispdbConfig struct {
	XMLName  xml.Name `xml:"clientConfig"`
	Provider struct {
		Incoming []ispdbServer `xml:"incomingServer"`
		Outgoing []ispdbServer `xml:"outgoingServer"`
	} `xml:"emailProvider"`
}

type ispdbServer struct {
	Type       string `xml:"type,attr"`
	Hostname   string `xml:"hostname"`
	Port       int    `xml:"port"`
	SocketType string `xml:"socketType"`
	Username   string `xml:"username"`
}

// fromISPDB asks Mozilla's database. Off by configuration for anyone who would rather the query
// not leave the machine — the domain being configured is what it reveals.
func fromISPDB(ctx context.Context, domain, address string) (Guess, bool) {
	url := "https://autoconfig.thunderbird.net/v1.1/" + domain
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Guess{}, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Guess{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Guess{}, false
	}

	var cfg ispdbConfig
	// Bounded: this is the one input to the program that comes off the network from somebody
	// else's server, and an unbounded decoder turns a slow answer into an unbounded allocation.
	// A provider record that does not fit in a megabyte is not a provider record.
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cfg); err != nil {
		return Guess{}, false
	}

	var g Guess
	if s, ok := pickServer(cfg.Provider.Incoming, "imap"); ok {
		g.IMAP = account.Server{Host: s.Hostname, Port: s.Port, Security: security(s.SocketType),
			LoginUser: expand(s.Username, address)}
	}
	if s, ok := pickServer(cfg.Provider.Outgoing, "smtp"); ok {
		g.SMTP = account.Server{Host: s.Hostname, Port: s.Port, Security: security(s.SocketType),
			LoginUser: expand(s.Username, address)}
	}
	if !g.Found() {
		return g, false
	}
	g.Source = "Mozilla's provider database"
	return g, true
}

// pickServer prefers a TLS-from-the-start entry of the right type, because a database that lists
// both is listing a preference, and the encrypted one is it.
func pickServer(list []ispdbServer, want string) (ispdbServer, bool) {
	var fallback ispdbServer
	var haveFallback bool
	for _, s := range list {
		if s.Type != "" && !strings.EqualFold(s.Type, want) {
			continue
		}
		if strings.EqualFold(s.SocketType, "SSL") {
			return s, true
		}
		if !haveFallback {
			fallback, haveFallback = s, true
		}
	}
	return fallback, haveFallback
}

func security(socketType string) account.Security {
	if strings.EqualFold(socketType, "STARTTLS") {
		return account.STARTTLS
	}
	return account.TLS
}

// expand substitutes the placeholders the database uses for the address.
func expand(username, address string) string {
	if username == "" {
		return ""
	}
	local := address
	if i := strings.LastIndex(address, "@"); i >= 0 {
		local = address[:i]
	}
	r := strings.NewReplacer("%EMAILADDRESS%", address, "%EMAILLOCALPART%", local)
	out := r.Replace(username)
	if out == address {
		return "" // the default; storing it would be noise in the registry
	}
	return out
}

// probe is the last resort: try the conventional hostnames and — critically — READ THE GREETING.
//
// A bare TCP accept proves nothing on shared hosting, where a wildcard vhost answers on every name
// that resolves. The greeting is what makes the probe honest: `* OK` from IMAP, `220` from SMTP,
// or the candidate is discarded however cleanly it connected.
func probe(ctx context.Context, domain string) (Guess, bool) {
	g := greetAny(ctx,
		[]string{"mail." + domain, "imap." + domain, domain},
		[]string{"mail." + domain, "smtp." + domain, domain})
	if !g.Found() {
		return g, false
	}
	g.Source = "probed — the server answered on a conventional name"
	g.Note = "This was guessed by trying common hostnames. If the certificate names a different " +
		"host, use that name instead: shared hosting usually issues one certificate for the " +
		"hosting server, not for your domain."
	return g, true
}

// greetAny asks every candidate AT ONCE and keeps the most preferred one that answered.
//
// Concurrent because the alternative measured badly rather than theoretically: a dead name costs
// the full three-second dial timeout, and a domain whose mail lives elsewhere fails all of them.
// Six names in a row is eighteen seconds — longer than the wizard is willing to wait — so a lookup
// that would have succeeded on a later strategy was cut off before it ever got there. Asking in
// parallel makes the whole round cost one timeout instead of six.
//
// Order still decides: every answer is collected and the earliest candidate in the list wins, so
// "mail.<domain>" beats the bare domain even when the bare domain replies first.
func greetAny(ctx context.Context, imapHosts, smtpHosts []string) Guess {
	var g Guess
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if h := firstGreeter(ctx, imapHosts, 993, "* OK"); h != "" {
			g.IMAP = account.Server{Host: h, Port: 993, Security: account.TLS}
		}
	}()
	go func() {
		defer wg.Done()
		if h := firstGreeter(ctx, smtpHosts, 465, "220"); h != "" {
			g.SMTP = account.Server{Host: h, Port: 465, Security: account.TLS}
		}
	}()
	wg.Wait()
	return g
}

func firstGreeter(ctx context.Context, hosts []string, port int, want string) string {
	type answer struct {
		i  int
		ok bool
	}
	ch := make(chan answer, len(hosts))
	for i, h := range hosts {
		go func(i int, h string) { ch <- answer{i, greets(ctx, h, port, want)} }(i, h)
	}
	best := -1
	for range hosts {
		if a := <-ch; a.ok && (best < 0 || a.i < best) {
			best = a.i
		}
	}
	if best < 0 {
		return ""
	}
	return hosts[best]
}

func greets(ctx context.Context, host string, port int, want string) bool {
	d := net.Dialer{Timeout: 3 * time.Second}
	// InsecureSkipVerify is on FOR THE PROBE ONLY, and it is not a shortcut: the certificate
	// on a shared host names the hosting server, not the domain being probed, so verifying
	// here would reject exactly the candidate that is correct. Nothing is sent over this
	// connection and nothing from it is trusted — the real check verifies properly later.
	conn, err := tlsDial(ctx, &d, host, port)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 128)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(buf[:n])), want)
}

func addr(host string, port int) string { return fmt.Sprintf("%s:%d", host, port) }
