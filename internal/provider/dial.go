package provider

import (
	"context"
	"crypto/tls"
	"net"
)

// tlsDial is the probe's connection, and the one place InsecureSkipVerify appears in this program.
//
// It is here rather than inline so the exception has exactly one home and a comment that cannot
// drift away from it: the certificate on a shared host names the hosting server, not the domain
// being probed, so verifying at this step would reject precisely the candidate that turns out to
// be right. Nothing is sent over this connection, nothing read from it is believed beyond "some
// mail server answered", and the real check in internal/verify validates the certificate properly
// before a single byte of password is written anywhere.
func tlsDial(ctx context.Context, d *net.Dialer, host string, port int) (net.Conn, error) {
	raw, err := d.DialContext(ctx, "tcp", addr(host, port))
	if err != nil {
		return nil, err
	}
	c := tls.Client(raw, &tls.Config{ServerName: host, InsecureSkipVerify: true}) //nolint:gosec // see above
	if err := c.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return c, nil
}
