// Package domain holds the pure core types of layerlens: digests, references,
// image records, per-layer changesets and the cumulative/diff trees.
//
// It depends on the standard library only. In particular it must never import
// net/http or go-containerregistry: every algorithm that operates on these
// types is unit-testable from hand-built structs (ARCHITECTURE §2).
package domain

import (
	"encoding/hex"
	"fmt"
	"hash"
)

// DigestPrefix is the only algorithm layerlens produces or accepts.
const DigestPrefix = "sha256:"

// digestHexLen is the length of a sha256 digest in lowercase hex.
const digestHexLen = 64

// Digest is a content digest in canonical OCI string form,
// `sha256:<64 lowercase hex>`.
//
// Three different digests appear in this codebase and they are never
// interchangeable (ARCHITECTURE §3, DECISIONS A4):
//
//   - the *compressed* digest identifies a blob in a registry;
//   - the *DiffID* is the digest of the uncompressed layer tar and drives all
//     layer-cache sharing (trunk / ChainID) logic;
//   - the *normalized changeset digest* (§3.1) is ours, and answers only
//     "could these two layers have been the same layer?".
type Digest string

// ParseDigest validates s and returns it as a Digest.
func ParseDigest(s string) (Digest, error) {
	d := Digest(s)
	if err := d.Validate(); err != nil {
		return "", err
	}
	return d, nil
}

// MustDigest is ParseDigest for constants and tests; it panics on an invalid
// digest.
func MustDigest(s string) Digest {
	d, err := ParseDigest(s)
	if err != nil {
		panic(err)
	}
	return d
}

// DigestFromBytes renders a raw 32-byte sha256 sum as a Digest.
func DigestFromBytes(sum []byte) Digest {
	return Digest(DigestPrefix + hex.EncodeToString(sum))
}

// DigestFromHash renders the current sum of h as a Digest. h must be a sha256
// hash.
func DigestFromHash(h hash.Hash) Digest {
	return DigestFromBytes(h.Sum(nil))
}

// Validate reports whether d matches ^sha256:[a-f0-9]{64}$.
//
// Every digest that becomes a filesystem path component is validated here
// before any filepath.Join (ARCHITECTURE §7.3), so this check is a security
// control and not merely a sanity check.
func (d Digest) Validate() error {
	s := string(d)
	if len(s) != len(DigestPrefix)+digestHexLen {
		return fmt.Errorf("invalid digest %q: want %d characters", s, len(DigestPrefix)+digestHexLen)
	}
	if s[:len(DigestPrefix)] != DigestPrefix {
		return fmt.Errorf("invalid digest %q: want %q prefix", s, DigestPrefix)
	}
	for i := len(DigestPrefix); i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("invalid digest %q: non lowercase-hex byte at offset %d", s, i)
		}
	}
	return nil
}

// IsValid reports whether d is a well-formed digest.
func (d Digest) IsValid() bool { return d.Validate() == nil }

// Hex returns the hex half of the digest, or "" if d is malformed. This is the
// only part that may be used as a path component.
func (d Digest) Hex() string {
	if !d.IsValid() {
		return ""
	}
	return string(d)[len(DigestPrefix):]
}

// Short returns the conventional 12-hex-character display abbreviation, or ""
// if d is malformed.
func (d Digest) Short() string {
	h := d.Hex()
	if h == "" {
		return ""
	}
	return h[:12]
}
