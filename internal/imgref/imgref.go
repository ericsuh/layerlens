// Package imgref parses user-supplied image references and validates them
// against the registry allowlist (ARCHITECTURE §7.1).
//
// It is one of only two packages allowed to import go-containerregistry, and
// none of its types escape: the result is a domain.ImageRef.
//
// The allowlist is the first half of the trust boundary. It governs *which
// registry a user may name*; it deliberately says nothing about which IP the
// process ends up talking to, because a blob GET legitimately redirects to a
// CDN whose host cannot be enumerated. That second half is internal/safehttp's
// per-connection address screen. Neither control is relied on alone.
package imgref

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/ericsuh/layerlens/internal/domain"
)

// ErrInvalidReference reports a reference that does not parse at all.
var ErrInvalidReference = errors.New("imgref: not a valid image reference")

// ErrRegistryNotAllowed reports a reference whose registry host is not on the
// allowlist. It carries the host so the API can name it back to the user.
type ErrRegistryNotAllowed struct {
	Registry string
}

func (e *ErrRegistryNotAllowed) Error() string {
	return fmt.Sprintf("imgref: registry %q is not on the allowlist", e.Registry)
}

// DefaultPatterns is the ARCHITECTURE §7.1 allowlist, extended with
// "*.pkg.dev" per RESEARCH Q10 (Artifact Registry superseded GCR).
//
// A "*" matches one or more whole dot-separated labels and never part of one,
// which is the entire point: "evilgcr.io" must not match "*.gcr.io" and
// "x.azurecr.io.evil.com" must not match "*.azurecr.io".
var DefaultPatterns = []string{
	// Docker Hub, in the three spellings go-containerregistry can produce.
	"docker.io",
	"index.docker.io",
	"registry-1.docker.io",
	"ghcr.io",
	// GCR and its regional mirrors, then Artifact Registry (RESEARCH Q10).
	"gcr.io",
	"*.gcr.io",
	"*.pkg.dev",
	// ECR Public is the only anonymously pullable ECR (DECISIONS A1); the
	// private form is allowlisted for completeness and will fail auth.
	"public.ecr.aws",
	"*.dkr.ecr.*.amazonaws.com",
	"*.azurecr.io",
}

// Allowlist decides whether a registry host may be contacted.
type Allowlist struct {
	patterns []string
}

// NewAllowlist builds an allowlist from host patterns. Patterns are matched on
// whole label boundaries (see DefaultPatterns).
func NewAllowlist(patterns []string) *Allowlist {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return &Allowlist{patterns: out}
}

// Default returns the standard allowlist.
func Default() *Allowlist { return NewAllowlist(DefaultPatterns) }

// Patterns returns the allowlist as configured, for display in the API and UI.
// The UI must not carry a second, drifting copy of this list.
func (a *Allowlist) Patterns() []string {
	out := make([]string, len(a.patterns))
	copy(out, a.patterns)
	return out
}

// Allows reports whether host is on the allowlist. host must already be a bare
// registry host with no port.
func (a *Allowlist) Allows(host string) bool {
	host = strings.ToLower(host)
	// A trailing dot makes "ghcr.io." the same name to DNS but a different
	// string here; normalizing it away closes that spelling.
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return false
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if l == "" {
			return false
		}
	}
	for _, p := range a.patterns {
		if matchLabels(strings.Split(p, "."), labels) {
			return true
		}
	}
	return false
}

// matchLabels matches a label-wise pattern against a host's labels, where "*"
// consumes one or more whole labels.
//
// Written as a backtracking match rather than a regexp so that the rule stays
// legible: there is no way to accidentally allow a "*" to eat half a label,
// which is exactly the bug that makes "evilgcr.io" pass a naive suffix check.
func matchLabels(pattern, labels []string) bool {
	if len(pattern) == 0 {
		return len(labels) == 0
	}
	head := pattern[0]
	if head == "*" {
		// One or more labels: try every split point.
		for take := 1; take <= len(labels); take++ {
			if matchLabels(pattern[1:], labels[take:]) {
				return true
			}
		}
		return false
	}
	if len(labels) == 0 || labels[0] != head {
		return false
	}
	return matchLabels(pattern[1:], labels[1:])
}

// Parse validates a user-supplied reference and resolves it against the
// allowlist. It performs no network I/O whatsoever — it is what gates
// POST /api/v1/pulls before a socket is ever opened.
func (a *Allowlist) Parse(raw string) (domain.ImageRef, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return domain.ImageRef{}, fmt.Errorf("%w: the reference is empty", ErrInvalidReference)
	}
	// Default (strict) validation: name.WeakValidation would accept
	// uppercase repositories that no registry can serve, and neither
	// name.Insecure nor a default-registry override is ever passed, so the
	// scheme stays https for every non-localhost host.
	ref, err := name.ParseReference(trimmed)
	if err != nil {
		return domain.ImageRef{}, fmt.Errorf("%w: %s", ErrInvalidReference, trimmed)
	}

	// DNS is case-insensitive and a trailing root dot names the same host,
	// so both spellings are normalized away here rather than being carried
	// into the allowlist check, the pull manager's idempotency key and the
	// TLS ServerName downstream.
	registry := strings.TrimSuffix(strings.ToLower(ref.Context().RegistryStr()), ".")
	// An explicit port is refused rather than allowlisted per-port: the
	// allowlist names registries, and "ghcr.io:8443" is a different service
	// from the one the operator vetted.
	if _, _, ok := splitPort(registry); ok {
		return domain.ImageRef{}, fmt.Errorf(
			"%w: %s names an explicit port; layerlens pulls over https on port 443 only",
			ErrInvalidReference, trimmed)
	}
	// The allowlist verdict comes before the scheme check so that a
	// plainly-local reference ("localhost/x", "127.0.0.1/x") is reported as
	// what a user needs to hear — that host is not a registry layerlens may
	// pull from — rather than as a scheme technicality.
	if !a.Allows(registry) {
		return domain.ImageRef{}, &ErrRegistryNotAllowed{Registry: registry}
	}
	// Belt and braces against a future gcr change: a registry that would be
	// contacted over plaintext is refused outright.
	if scheme := ref.Context().Scheme(); scheme != "https" {
		return domain.ImageRef{}, fmt.Errorf("%w: %s would be fetched over %s, not https",
			ErrInvalidReference, trimmed, scheme)
	}

	out := domain.ImageRef{
		Raw:        trimmed,
		Registry:   registry,
		Repository: ref.Context().RepositoryStr(),
	}
	switch r := ref.(type) {
	case name.Tag:
		out.Tag = r.TagStr()
	case name.Digest:
		digest, err := domain.ParseDigest(r.DigestStr())
		if err != nil {
			return domain.ImageRef{}, fmt.Errorf("%w: %s has an unsupported digest", ErrInvalidReference, trimmed)
		}
		out.Digest = digest
	}
	return out, nil
}

// splitPort reports whether host carries an explicit ":port" suffix.
//
// net.SplitHostPort is not usable here: it rejects a bare host, and an IPv6
// literal in a registry position is not something any of the allowed
// registries can be spelled as.
func splitPort(host string) (string, string, bool) {
	i := strings.LastIndex(host, ":")
	if i < 0 {
		return host, "", false
	}
	return host[:i], host[i+1:], true
}

// Canonical renders a reference back into canonical "registry/repository:tag" (or
// "@digest") form. It is what the pull manager keys idempotency on, so two
// spellings of the same image are one pull.
func Canonical(ref domain.ImageRef) string {
	base := ref.Registry + "/" + ref.Repository
	if ref.Digest != "" {
		return base + "@" + string(ref.Digest)
	}
	if ref.Tag != "" {
		return base + ":" + ref.Tag
	}
	return base + ":latest"
}
