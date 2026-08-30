package safehttp_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/safehttp"
)

// tableResolver is the injectable resolver: no test in this package may
// depend on real DNS.
type tableResolver struct {
	table   map[string][]netip.Addr
	lookups atomic.Int64
}

func (r *tableResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.lookups.Add(1)
	addrs, ok := r.table[host]
	if !ok {
		return nil, fmt.Errorf("no such host: %s", host)
	}
	return addrs, nil
}

func addrs(t *testing.T, list ...string) []netip.Addr {
	t.Helper()
	out := make([]netip.Addr, 0, len(list))
	for _, s := range list {
		a, err := netip.ParseAddr(s)
		require.NoError(t, err)
		out = append(out, a)
	}
	return out
}

func TestScreenAddrRejectsNonPublic(t *testing.T) {
	refused := []string{
		// loopback
		"127.0.0.1", "127.9.9.9", "::1",
		// RFC1918 private
		"10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.254", "192.168.1.1",
		// link-local, incl. the cloud metadata address every SSRF wants
		"169.254.169.254", "169.254.0.1", "fe80::1",
		// unique-local IPv6
		"fc00::1", "fd00:ec2::254",
		// multicast
		"224.0.0.1", "239.255.255.250", "ff02::1", "ff01::1",
		// unspecified
		"0.0.0.0", "::",
		// IPv4-mapped IPv6 — unwrapped and re-screened
		"::ffff:10.0.0.1", "::ffff:127.0.0.1", "::ffff:169.254.169.254",
		// carrier-grade NAT and other reserved v4
		"100.64.0.1", "192.0.0.1", "198.18.0.1", "240.0.0.1", "255.255.255.255",
		// v6 transition forms that deliver to a private v4
		"64:ff9b::a00:1", "64:ff9b::a9fe:a9fe", "2002:0a00:0001::", "2001::1",
		// documentation/discard
		"2001:db8::1", "100::1",
	}
	for _, s := range refused {
		ip, err := netip.ParseAddr(s)
		require.NoError(t, err)
		err = safehttp.ScreenAddr(ip)
		assert.Error(t, err, "expected %s to be refused", s)
		assert.ErrorIs(t, err, safehttp.ErrForbiddenAddress)
	}

	allowed := []string{
		"93.184.216.34", "8.8.8.8", "1.1.1.1", "172.32.0.1", "172.15.255.255",
		"2606:2800:220:1:248:1893:25c8:1946", "2001:4860:4860::8888",
		"::ffff:93.184.216.34", "2002:5db8:d822::", // 6to4 wrapping a public v4
	}
	for _, s := range allowed {
		ip, err := netip.ParseAddr(s)
		require.NoError(t, err)
		assert.NoError(t, safehttp.ScreenAddr(ip), "expected %s to be allowed", s)
	}
}

func TestDialRejectsPrivateResolution(t *testing.T) {
	resolver := &tableResolver{table: map[string][]netip.Addr{
		"metadata.evil.test": addrs(t, "169.254.169.254"),
		"lan.evil.test":      addrs(t, "10.1.2.3"),
		"ula.evil.test":      addrs(t, "fc00::1"),
	}}
	client := safehttp.New(safehttp.Options{Resolver: resolver}).Client()

	for _, host := range []string{"metadata.evil.test", "lan.evil.test", "ula.evil.test"} {
		_, err := client.Get("https://" + host + "/x")
		require.Error(t, err)
		assert.ErrorIs(t, err, safehttp.ErrForbiddenAddress, "host %s", host)
	}
}

// A name that answers with one public and one private address is hostile: the
// public answer must not be "picked", the whole host must fail.
func TestDialRejectsMixedPublicAndPrivateAnswers(t *testing.T) {
	resolver := &tableResolver{table: map[string][]netip.Addr{
		"mixed.test":  addrs(t, "93.184.216.34", "169.254.169.254"),
		"mixed2.test": addrs(t, "10.0.0.5", "93.184.216.34"),
	}}
	client := safehttp.New(safehttp.Options{Resolver: resolver}).Client()

	for _, host := range []string{"mixed.test", "mixed2.test"} {
		_, err := client.Get("https://" + host + "/x")
		require.Error(t, err)
		assert.ErrorIs(t, err, safehttp.ErrForbiddenAddress, "host %s", host)
	}
}

func TestDialRejectsNonHTTPSPort(t *testing.T) {
	resolver := &tableResolver{table: map[string][]netip.Addr{
		"public.test": addrs(t, "93.184.216.34"),
	}}
	client := safehttp.New(safehttp.Options{Resolver: resolver}).Client()

	_, err := client.Get("https://public.test:8443/x")
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrForbiddenPort)
	// The port refusal happens before any resolution: a refused destination
	// must not even become a DNS query.
	assert.Equal(t, int64(0), resolver.lookups.Load())
}

func TestPlaintextIsRefused(t *testing.T) {
	resolver := &tableResolver{table: map[string][]netip.Addr{
		"public.test": addrs(t, "93.184.216.34"),
	}}
	client := safehttp.New(safehttp.Options{Resolver: resolver}).Client()

	_, err := client.Get("http://public.test/x")
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrPlaintextRefused)
	assert.Equal(t, int64(0), resolver.lookups.Load())
}

// The literal-IP dial is the anti-rebinding property: exactly one resolution
// per connection, and the address that was screened is the one dialed.
//
// httptest's certificate carries a DNS SAN for "example.com", so the request
// below really does complete against a name that resolved to the loopback
// literal — proving the ServerName used for verification is the *name* while
// the socket went to the vetted address.
func TestDialsVettedLiteralWithOneResolution(t *testing.T) {
	server := newLoopbackTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	_, port := splitHostPort(t, server.URL)

	resolver := &tableResolver{table: map[string][]netip.Addr{
		"example.com": addrs(t, "127.0.0.1"),
	}}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client := safehttp.New(safehttp.Options{
		Resolver:       resolver,
		PermitLoopback: true,
		RootCAs:        pool,
	}).Client()

	resp, err := client.Get(fmt.Sprintf("https://example.com:%s/", port))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, int64(1), resolver.lookups.Load(),
		"exactly one resolution per connection — a second lookup is a rebinding window")
}

// A redirect to a private address must fail at the dial, not be followed.
// This is the CDN-redirect tension of §7.2 resolved in the only place it can
// be: every hop goes through the same guarded dialer.
func TestRedirectToPrivateAddressIsRefused(t *testing.T) {
	origin := newLoopbackTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/blob" {
			http.Redirect(w, r, "https://cdn.evil.test/blob", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "unexpected")
	})
	client, _ := loopbackClient(t, origin, map[string][]netip.Addr{
		"cdn.evil.test": addrs(t, "10.9.9.9"),
	})

	_, err := client.Get(origin.URL + "/v2/blob")
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrForbiddenAddress)
}

// A redirect to a *public* address is followed: blob CDNs are legitimate and
// unavoidable, and a policy that broke them would be turned off.
func TestRedirectToPublicAddressIsFollowed(t *testing.T) {
	cdn := newLoopbackTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "blob-bytes")
	})
	origin := newLoopbackTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cdn.URL+"/blob", http.StatusFound)
	})
	client, _ := loopbackClient(t, origin, nil)

	resp, err := client.Get(origin.URL + "/v2/blob")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "blob-bytes", string(body))
}

func TestRedirectToPlaintextIsRefused(t *testing.T) {
	origin := newLoopbackTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://cdn.public.test/blob", http.StatusFound)
	})
	client, _ := loopbackClient(t, origin, map[string][]netip.Addr{
		"cdn.public.test": addrs(t, "93.184.216.34"),
	})

	_, err := client.Get(origin.URL + "/v2/blob")
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrPlaintextRefused)
}

func TestRedirectChainIsCapped(t *testing.T) {
	var server *httptest.Server
	var hops atomic.Int64
	server = newLoopbackTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, server.URL+"/next", http.StatusFound)
	})
	client, _ := loopbackClient(t, server, nil)

	_, err := client.Get(server.URL + "/start")
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrTooManyRedirects)
	assert.LessOrEqual(t, hops.Load(), int64(safehttp.MaxRedirects+1))
}

func TestManifestResponseSizeIsCapped(t *testing.T) {
	const limit = 4096
	server := newLoopbackTLSServer(t, func(w http.ResponseWriter, r *http.Request) {
		size := limit * 4
		if strings.Contains(r.URL.Path, "exact") {
			size = limit
		}
		// No Content-Length: the streaming cap, not the declared one.
		w.Header().Set("Transfer-Encoding", "chunked")
		for written := 0; written < size; written += 256 {
			_, _ = w.Write(make([]byte, 256))
		}
	})
	client, _ := loopbackClientWithLimit(t, server, nil, limit)

	resp, err := client.Get(server.URL + "/v2/library/alpine/manifests/3.20")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrResponseTooLarge)

	// A body of exactly the limit is legal.
	resp2, err := client.Get(server.URL + "/v2/library/alpine/manifests/exact")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	body, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Len(t, body, limit)
}

func TestDeclaredContentLengthOverLimitIsRefused(t *testing.T) {
	const limit = 1024
	server := newLoopbackTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "999999")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 999999))
	})
	client, _ := loopbackClientWithLimit(t, server, nil, limit)

	_, err := client.Get(server.URL + "/v2/x/manifests/latest")
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrResponseTooLarge)
}

// Layer blobs are the one uncapped case; a blob the caller declares small (the
// image config) is capped like a manifest.
func TestBlobCapAppliesOnlyToDeclaredSmallBlobs(t *testing.T) {
	const limit = 1024
	const digest = "aaaabbbbccccddddeeeeffff00001111aaaabbbbccccddddeeeeffff00001111a"
	server := newLoopbackTLSServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, limit*4))
	})
	client, transport := loopbackClientWithLimit(t, server, nil, limit)

	big, err := client.Get(server.URL + "/v2/x/blobs/sha256:0123456789")
	require.NoError(t, err)
	defer func() { _ = big.Body.Close() }()
	body, err := io.ReadAll(big.Body)
	require.NoError(t, err, "a layer blob must not be capped")
	assert.Len(t, body, limit*4)

	release := transport.ExpectSmallBlob("sha256:" + digest)
	defer release()
	small, err := client.Get(server.URL + "/v2/x/blobs/sha256:" + digest)
	require.NoError(t, err)
	defer func() { _ = small.Body.Close() }()
	_, err = io.ReadAll(small.Body)
	require.Error(t, err)
	assert.ErrorIs(t, err, safehttp.ErrResponseTooLarge)

	// After release the same URL is a plain blob again.
	release()
	after, err := client.Get(server.URL + "/v2/x/blobs/sha256:" + digest)
	require.NoError(t, err)
	defer func() { _ = after.Body.Close() }()
	_, err = io.ReadAll(after.Body)
	assert.NoError(t, err)
}

func TestPermitLoopbackDefaultsOff(t *testing.T) {
	// The escape hatch the tests in this file rely on must be off unless a
	// caller explicitly asks. cmd/layerlens never does.
	client := safehttp.New(safehttp.Options{Resolver: &tableResolver{}}).Client()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	_, err := client.Get(server.URL + "/")
	require.Error(t, err)
	assert.True(t,
		errors.Is(err, safehttp.ErrForbiddenAddress) || errors.Is(err, safehttp.ErrForbiddenPort),
		"expected a refusal, got %v", err)
}

// --- helpers ---------------------------------------------------------------

func splitHostPort(t *testing.T, raw string) (string, string) {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Hostname(), u.Port()
}

func newLoopbackTLSServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	return server
}

func loopbackClient(t *testing.T, server *httptest.Server, table map[string][]netip.Addr) (*http.Client, *safehttp.Transport) {
	t.Helper()
	return loopbackClientWithLimit(t, server, table, 0)
}

// loopbackClientWithLimit builds a client that trusts the httptest server's
// certificate and is allowed to reach loopback, so a local server can stand in
// for a registry. Every other control — the address screen for non-loopback
// answers, the port rule, the plaintext refusal, the redirect policy, the body
// caps — is the production one, and the request goes through the production
// RoundTrip.
func loopbackClientWithLimit(t *testing.T, server *httptest.Server, table map[string][]netip.Addr, limit int64) (*http.Client, *safehttp.Transport) {
	t.Helper()
	if table == nil {
		table = map[string][]netip.Addr{}
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport := safehttp.New(safehttp.Options{
		Resolver:         &tableResolver{table: table},
		PermitLoopback:   true,
		MaxMetadataBytes: limit,
		RootCAs:          pool,
	})
	return transport.Client(), transport
}
