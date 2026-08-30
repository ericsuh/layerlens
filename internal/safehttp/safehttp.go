// Package safehttp builds the only outbound HTTP transport layerlens uses.
//
// It is the second half of the §7 trust boundary. internal/imgref decides
// which registry a user may *name*; this package decides which address the
// process may actually *connect to*, on every single socket it opens —
// including the token-auth endpoint and every hop of the blob CDN redirect
// chain that no allowlist could ever enumerate.
//
// Three properties carry the security argument:
//
//  1. **One resolution per connection, then dial the vetted literal IP.**
//     Screening a hostname and then handing the *name* back to the dialer
//     leaves a DNS-rebinding window between the two lookups. Here the address
//     that was screened is the address that is dialed, so there is no window.
//
//  2. **Reject if ANY resolved address is non-public**, not "pick a public
//     one": a name that answers with both 93.184.216.34 and 169.254.169.254 is
//     hostile, and choosing the public answer would let a retry land on the
//     other one.
//
//  3. **Plaintext cannot be dialed at all.** DialContext — the hook
//     net/http uses for http:// — always fails. That makes an https→http
//     downgrade impossible regardless of which http.Client follows the
//     redirect, which matters because go-containerregistry builds its own
//     client and our CheckRedirect never runs on it.
package safehttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Errors returned by the guarded dialer. They are deliberately terse: the
// message reaches the user through the pull error, and a resolved private
// address is not something to echo back in detail.
var (
	// ErrForbiddenAddress reports a destination that resolved to a
	// non-public address (loopback, private, link-local, multicast, ...).
	ErrForbiddenAddress = errors.New("safehttp: destination address is not a public unicast address")
	// ErrForbiddenPort reports a destination port other than 443.
	ErrForbiddenPort = errors.New("safehttp: only https on port 443 may be dialed")
	// ErrPlaintextRefused reports an attempted plaintext (http://) request.
	ErrPlaintextRefused = errors.New("safehttp: plaintext http is refused; https only")
	// ErrNoAddresses reports a name that resolved to nothing usable.
	ErrNoAddresses = errors.New("safehttp: host resolved to no addresses")
	// ErrTooManyRedirects reports a redirect chain past the cap.
	ErrTooManyRedirects = errors.New("safehttp: too many redirects")
	// ErrResponseTooLarge reports a response body past its cap.
	ErrResponseTooLarge = errors.New("safehttp: response body exceeds the size limit")
)

// Defaults for the transport. Every one of them is a security control, not a
// tuning knob, so they live here as named constants rather than as literals.
const (
	// MaxRedirects caps a redirect chain. Blob GETs legitimately redirect
	// to a CDN (often twice); ten is generous and still finite.
	MaxRedirects = 10
	// MaxMetadataBytes caps every response that is not a blob fetch:
	// manifests, indexes, token responses, the /v2/ ping (§7.2).
	MaxMetadataBytes = 8 << 20
	// dialTimeout bounds one TCP connect.
	dialTimeout = 10 * time.Second
	// tlsHandshakeTimeout bounds one TLS handshake.
	tlsHandshakeTimeout = 10 * time.Second
	// responseHeaderTimeout bounds time-to-first-byte. It must not bound
	// the body: a 25 GiB layer legitimately takes a long time to stream.
	responseHeaderTimeout = 30 * time.Second
	idleConnTimeout       = 90 * time.Second
	maxIdleConns          = 32
)

// Resolver resolves a host name to IP addresses. *net.Resolver satisfies it;
// tests substitute a table.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options configures a Transport.
type Options struct {
	// Resolver defaults to net.DefaultResolver.
	Resolver Resolver
	// MaxMetadataBytes defaults to MaxMetadataBytes.
	MaxMetadataBytes int64
	// RootCAs overrides the system certificate pool. Production leaves it
	// nil; tests point it at a httptest server's own certificate so a
	// local stand-in registry can be reached without weakening
	// verification (InsecureSkipVerify is never set, anywhere).
	RootCAs *x509.CertPool

	// PermitLoopback relaxes the address screen and the port restriction
	// for loopback destinations.
	//
	// TESTS ONLY. It exists so a httptest server can stand in for a
	// registry; every other guarantee in this package still applies. It is
	// never set by cmd/layerlens (asserted by a test), and no flag or
	// environment variable can turn it on.
	PermitLoopback bool
}

// Transport is the SSRF-hardened outbound transport. Use New and hand the
// result to go-containerregistry via remote.WithTransport, or use Client for
// requests layerlens makes itself.
type Transport struct {
	base     *http.Transport
	resolver Resolver
	opts     Options
	maxMeta  int64

	// smallBlobs holds blob digests the caller has declared small — in
	// practice the image config, which §7.2 requires be size-capped like a
	// manifest. Registered by the ingest path around the config read; see
	// ExpectSmallBlob.
	smallBlobs sync.Map // map[string]struct{}, keyed by the 64-hex digest
}

// New builds the transport.
func New(opts Options) *Transport {
	t := &Transport{
		resolver: opts.Resolver,
		opts:     opts,
		maxMeta:  opts.MaxMetadataBytes,
	}
	if t.resolver == nil {
		t.resolver = net.DefaultResolver
	}
	if t.maxMeta <= 0 {
		t.maxMeta = MaxMetadataBytes
	}
	t.base = &http.Transport{
		// No proxy, ever. Proxy settings come from the environment, and
		// an environment-supplied proxy is both an address the screen
		// never sees and, on most hosts, a loopback one.
		Proxy: nil,
		// The hook net/http uses for plaintext. Failing it closed is
		// what makes an https→http downgrade unreachable even through a
		// redirect followed by somebody else's http.Client.
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, ErrPlaintextRefused
		},
		DialTLSContext:        t.dialTLS,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return t
}

// RoundTrip implements http.RoundTripper. It applies the §7.2 body caps on
// top of the guarded dialer.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL != nil && req.URL.Scheme != "https" {
		// Belt and braces with the plaintext dialer: this refusal names
		// the reason, which the dial-time one cannot.
		return nil, fmt.Errorf("%w: %s", ErrPlaintextRefused, req.URL.Scheme)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	limit, capped := t.bodyLimit(req)
	if !capped {
		return resp, nil
	}
	if resp.ContentLength > limit {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: %s declared %d bytes, limit %d",
			ErrResponseTooLarge, req.URL.Path, resp.ContentLength, limit)
	}
	resp.Body = &limitedBody{inner: resp.Body, remaining: limit}
	return resp, nil
}

// CloseIdleConnections implements the optional http.RoundTripper extension so
// a shutdown does not leak sockets.
func (t *Transport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// ExpectSmallBlob declares that the blob with this digest is metadata (the
// image config) and must be size-capped like a manifest. It returns a release
// function; call it once the blob has been read.
//
// The registry protocol gives no other way to tell a config blob from a layer
// blob at the transport: both are GETs under /blobs/, and after the CDN
// redirect the URL is not even on the registry's host any more. Matching on
// the digest appearing in the path is what every registry and CDN in practice
// preserves, and failing to match degrades to "not capped", never to a broken
// pull.
func (t *Transport) ExpectSmallBlob(hexDigest string) func() {
	hexDigest = strings.TrimPrefix(hexDigest, "sha256:")
	if hexDigest == "" {
		return func() {}
	}
	t.smallBlobs.Store(hexDigest, struct{}{})
	return func() { t.smallBlobs.Delete(hexDigest) }
}

// bodyLimit reports the cap that applies to this request's response body.
//
// Layer blobs are the one thing that must stay uncapped — they are unbounded
// by nature — so the rule is inverted: everything is capped unless it is a
// blob fetch, and a blob fetch the caller has declared small is capped anyway.
func (t *Transport) bodyLimit(req *http.Request) (int64, bool) {
	if req.URL == nil {
		return t.maxMeta, true
	}
	path := req.URL.Path
	if !strings.Contains(path, "/blobs/") {
		return t.maxMeta, true
	}
	capped := false
	t.smallBlobs.Range(func(key, _ any) bool {
		if hex, ok := key.(string); ok && strings.Contains(path, hex) {
			capped = true
			return false
		}
		return true
	})
	return t.maxMeta, capped
}

// dialTLS is the guarded dialer: resolve once, screen every answer, dial the
// vetted literal address, then hand-shake TLS against the *name*.
func (t *Transport) dialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("safehttp: malformed address %q: %w", addr, err)
	}

	addrs, err := t.screenedAddrs(ctx, network, host, port)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: dialTimeout}
	var lastErr error
	for _, ip := range addrs {
		conn, err := dialer.DialContext(ctx, tcpNetwork(ip), net.JoinHostPort(ip.String(), port))
		if err != nil {
			lastErr = err
			continue
		}
		// ServerName is the *name*, not the literal we dialed: dialing
		// the vetted IP must not cost certificate verification.
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: host,
			RootCAs:    t.opts.RootCAs,
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"},
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = tlsConn.Close()
			lastErr = err
			continue
		}
		return tlsConn, nil
	}
	if lastErr == nil {
		lastErr = ErrNoAddresses
	}
	return nil, lastErr
}

// screenedAddrs resolves host and returns its addresses only if every single
// one of them is safe to contact.
func (t *Transport) screenedAddrs(ctx context.Context, network, host, port string) ([]netip.Addr, error) {
	loopbackOK := t.opts.PermitLoopback
	if !loopbackOK && port != "443" {
		return nil, fmt.Errorf("%w: port %s", ErrForbiddenPort, port)
	}

	var addrs []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		// An IP literal is not resolved at all; it is screened directly.
		addrs = []netip.Addr{literal}
	} else {
		resolved, err := t.resolver.LookupNetIP(ctx, lookupNetwork(network), host)
		if err != nil {
			return nil, fmt.Errorf("safehttp: resolve %s: %w", host, err)
		}
		addrs = resolved
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAddresses, host)
	}

	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		unmapped := a.Unmap()
		if loopbackOK && unmapped.IsLoopback() {
			out = append(out, unmapped)
			continue
		}
		if err := ScreenAddr(unmapped); err != nil {
			// Reject the whole host, not just this answer: a name
			// that mixes public and private answers is hostile, and
			// silently using the public one would let a retry (or a
			// second connection) land on the other.
			return nil, fmt.Errorf("%w: %s resolved to %s", ErrForbiddenAddress, host, unmapped)
		}
		if port != "443" {
			// Reachable only with PermitLoopback set, where the
			// early check above is skipped: a test's loopback
			// server may use any port, a real host may not.
			return nil, fmt.Errorf("%w: port %s", ErrForbiddenPort, port)
		}
		out = append(out, unmapped)
	}
	return out, nil
}

// tcpNetwork narrows "tcp" to the family of the address actually being dialed,
// so a vetted IPv6 answer is not re-resolved by the dialer.
func tcpNetwork(ip netip.Addr) string {
	if ip.Is4() {
		return "tcp4"
	}
	return "tcp6"
}

// lookupNetwork maps a dial network onto the LookupNetIP network argument.
func lookupNetwork(network string) string {
	switch network {
	case "tcp4", "udp4", "ip4":
		return "ip4"
	case "tcp6", "udp6", "ip6":
		return "ip6"
	default:
		return "ip"
	}
}

// Non-public IPv4 ranges beyond what netip.Addr's own predicates cover.
var reservedV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT — a carrier's private space
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes 255.255.255.255
}

// IPv6 ranges that embed or tunnel an IPv4 address, or are otherwise not
// public unicast. The embedding ones matter: 64:ff9b::a00:1 is a perfectly
// ordinary-looking IPv6 address that a NAT64 gateway will deliver to 10.0.0.1.
var (
	nat64  = netip.MustParsePrefix("64:ff9b::/96")
	teredo = netip.MustParsePrefix("2001::/32")
	sixTo4 = netip.MustParsePrefix("2002::/16")
	// 2001:db8::/32 documentation, 5f00::/16 (former 6bone), 100::/64
	// discard-only.
	reservedV6 = []netip.Prefix{
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
)

// ScreenAddr reports why an address may not be dialed, or nil if it is an
// ordinary public unicast address.
//
// Exported because it is the single definition of "safe to contact" and is
// table-tested directly; the dialer is its only production caller.
func ScreenAddr(ip netip.Addr) error {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return fmt.Errorf("%w: invalid address", ErrForbiddenAddress)
	}
	if ip.Zone() != "" {
		// A zone means a link-scoped address; there is no public
		// unicast destination that needs one.
		return fmt.Errorf("%w: %s carries a zone", ErrForbiddenAddress, ip)
	}
	switch {
	case ip.IsLoopback(),
		ip.IsPrivate(), // RFC1918 and RFC4193 fc00::/7 unique-local
		ip.IsLinkLocalUnicast(),
		ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(),
		ip.IsMulticast(),
		ip.IsUnspecified():
		return fmt.Errorf("%w: %s", ErrForbiddenAddress, ip)
	}
	if ip.Is4() {
		for _, p := range reservedV4 {
			if p.Contains(ip) {
				return fmt.Errorf("%w: %s", ErrForbiddenAddress, ip)
			}
		}
		return nil
	}
	// IPv6 from here down.
	for _, p := range reservedV6 {
		if p.Contains(ip) {
			return fmt.Errorf("%w: %s", ErrForbiddenAddress, ip)
		}
	}
	if teredo.Contains(ip) {
		return fmt.Errorf("%w: %s is a Teredo tunnel address", ErrForbiddenAddress, ip)
	}
	if nat64.Contains(ip) || sixTo4.Contains(ip) {
		// Both embed an IPv4 address that the network will deliver to.
		// Screening only the outer form would let 64:ff9b::a9fe:a9fe
		// reach 169.254.169.254.
		embedded := embeddedV4(ip)
		if !embedded.IsValid() {
			return fmt.Errorf("%w: %s", ErrForbiddenAddress, ip)
		}
		if err := ScreenAddr(embedded); err != nil {
			return fmt.Errorf("%w: %s embeds %s", ErrForbiddenAddress, ip, embedded)
		}
	}
	return nil
}

// embeddedV4 extracts the IPv4 address a NAT64 (last 32 bits) or 6to4 (bits
// 16..47) address carries.
func embeddedV4(ip netip.Addr) netip.Addr {
	b := ip.As16()
	if sixTo4.Contains(ip) {
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
	}
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
}

// Client returns an http.Client using this transport, with the redirect policy
// of §7.2: capped chain, and no downgrade out of https.
//
// go-containerregistry constructs its own client for registry traffic, so this
// policy is not what protects those requests — the dialer is. This client is
// for requests layerlens makes itself.
func (t *Transport) Client() *http.Client {
	return &http.Client{
		Transport: t,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("%w: %d hops", ErrTooManyRedirects, len(via))
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("%w: redirect to %s", ErrPlaintextRefused, req.URL.Scheme)
			}
			return nil
		},
	}
}

// limitedBody fails a response body that runs past its cap instead of
// silently truncating it: a truncated manifest would be a parse error whose
// cause nobody could see.
type limitedBody struct {
	inner     io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, ErrResponseTooLarge
	}
	n, err := b.inner.Read(p)
	// The counter is allowed to reach exactly zero: a body of precisely
	// the limit is legal, and only the byte after it is not.
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n, ErrResponseTooLarge
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.inner.Close() }
