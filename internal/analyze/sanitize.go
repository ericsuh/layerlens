// Package analyze holds the pure algorithms over domain types: history
// mapping, the streaming layer indexer, the normalized changeset digest and
// ChainID computation.
//
// It imports domain, the standard library and a zstd decompressor. It must
// never import net/http or go-containerregistry (ARCHITECTURE §2): the
// registry/daemon adapters in internal/ingest hand this package plain readers.
package analyze

import (
	"path"
	"strings"
)

// RootPath is the sanitized form of the archive root.
const RootPath = "/"

// SanitizePath normalizes a tar entry name into the canonical Entry.Path form
// and reports whether the name is usable at all (ARCHITECTURE §7.3).
//
// The rules: reject names containing NUL; strip a leading "/" and any "./"
// prefix; path.Clean; reject an empty result, the bare current directory, and
// any result that still contains a ".." segment. The result is "/"-rooted with
// no trailing slash.
//
// layerlens never materializes tar entries on disk, so traversal cannot write
// anywhere. Sanitization protects the correctness of the in-memory tree and
// the honesty of the paths we display; callers record a warning in
// LayerIndex.Warnings and skip the entry when ok is false.
func SanitizePath(name string) (clean string, ok bool) {
	if strings.ContainsRune(name, 0) {
		return "", false
	}
	// Strip the leading separator and any "./" prefixes before cleaning so
	// that "/./foo" and "./foo" reduce identically.
	s := stripRootPrefixes(name)
	if s == "" || s == "." {
		return "", false
	}
	s = path.Clean(s)
	if s == "" || s == "." {
		return "", false
	}
	// path.Clean leaves leading ".." segments in place; anything that still
	// escapes the archive root is rejected outright rather than clamped, so
	// a hostile name can never be silently rewritten into a plausible one.
	if s == ".." || strings.HasPrefix(s, "../") {
		return "", false
	}
	return RootPath + s, true
}

// IsRootName reports whether a tar entry name denotes the archive root itself
// ("/", ".", "./" and friends). Such an entry carries no changeset
// information: the root directory always exists. The indexer skips it without
// a warning, which keeps the common `tar`-produced "./" member from making
// every layer look suspicious.
func IsRootName(name string) bool {
	if strings.ContainsRune(name, 0) {
		return false
	}
	s := stripRootPrefixes(strings.TrimSuffix(name, "/"))
	return s == "" || s == "."
}

// stripRootPrefixes removes leading "/" and "./" sequences from a tar name.
func stripRootPrefixes(s string) string {
	for {
		switch {
		case strings.HasPrefix(s, "/"):
			s = s[1:]
		case strings.HasPrefix(s, "./"):
			s = s[2:]
		default:
			return s
		}
	}
}
