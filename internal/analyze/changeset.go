package analyze

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"
	"strings"

	"github.com/ericsuh/layerlens/internal/domain"
)

// ChangesetSchemeVersion prefixes the hashed stream so that the definition of
// the digest can evolve without silently colliding with digests computed by an
// older definition. Bumping it changes every digest.
const ChangesetSchemeVersion byte = 1

// xattrPrefix is the PAX record namespace that carries extended attributes.
const xattrPrefix = "SCHILY.xattr."

// ChangesetDigest is the normalized changeset digest of a layer (ARCHITECTURE
// §3.1, RESEARCH Q9 — binding, superseding Q5).
//
// The field set is tarsum v1, i.e. literally what Docker/BuildKit hashes for
// its *build* cache (moby/buildkit cache/contenthash/{filehash,tarsum}.go:
// v1TarHeaderSelect takes the v0 field list and copies it "excluding the
// 'mtime' header", then appends sorted xattrs, and the file's content is
// streamed into the same hash). Each entry therefore contributes
//
//	Path, Kind (typeflag), Mode (12 permission bits), UID, GID, Uname, Gname,
//	Size, LinkTarget, Devmajor, Devminor, sorted Xattrs, ContentSHA
//
// and MtimeUnix is the ONLY excluded field. uid/gid ARE included: layerlens
// follows Docker's own rule rather than a judgement call of its own.
//
// Two layers with equal changeset digests are "could-be-shared": Docker's
// layer store treats them as distinct blobs (their DiffIDs differ), but under
// Docker's *build*-cache rule their contents are equivalent. This is NEVER a
// cache hit and must never be presented as one — that is what DiffID equality
// is for.
//
// The result is independent of the order in which entries are supplied: a copy
// is sorted by path first, so a layer's digest does not depend on tar ordering.
func ChangesetDigest(entries []domain.Entry) domain.Digest {
	return changesetDigest(ChangesetSchemeVersion, entries)
}

// changesetDigest is ChangesetDigest with the scheme version as a parameter,
// so that tests can prove the version byte really participates without any
// production code path being able to vary it.
func changesetDigest(version byte, entries []domain.Entry) domain.Digest {
	ordered := make([]int, len(entries))
	for i := range ordered {
		ordered[i] = i
	}
	sort.Slice(ordered, func(a, b int) bool {
		return entries[ordered[a]].Path < entries[ordered[b]].Path
	})

	h := sha256.New()
	// A scheme-version byte, not a length-prefixed field: it is metadata
	// about the encoding rather than part of any entry.
	_, _ = h.Write([]byte{version})
	writeUint(h, uint64(len(entries)))
	for _, i := range ordered {
		writeEntry(h, &entries[i])
	}
	return domain.DigestFromHash(h)
}

// writeEntry appends one entry's tarsum-v1 field selection to h. The field
// selection itself lives in fields.go and is shared verbatim with the diff
// tree's modification predicate (§3.2), so the digest and the tree can never
// disagree about what "same" means.
func writeEntry(h hash.Hash, e *domain.Entry) {
	writeFields(h, e.Path, fieldsOfEntry(e))

	// MtimeUnix is deliberately absent. See the doc comment above: it is the
	// one difference that does not make two layers different, and that
	// exclusion is the product thesis.
}

func writeStr(h hash.Hash, s string) {
	writeUint(h, uint64(len(s)))
	_, _ = h.Write([]byte(s))
}

func writeUint(h hash.Hash, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	_, _ = h.Write(buf[:])
}

func writeInt(h hash.Hash, v int64) {
	writeUint(h, uint64(v))
}

// FilterXattrs extracts the extended attributes from a tar header's PAX
// records and applies BuildKit's filter: keep "security.capability", drop
// every other "security.*" and "system.*" attribute, keep the rest.
//
// The dropped namespaces are ones the kernel or the extraction environment
// synthesizes (SELinux labels, POSIX ACL shadows), so including them would
// make two otherwise-identical layers look different for reasons that have
// nothing to do with what the build produced.
//
// It returns nil rather than an empty map when nothing survives, so that
// Entry stays compact and round-trips through the index codec unchanged.
func FilterXattrs(pax map[string]string) map[string]string {
	var out map[string]string
	for k, v := range pax {
		name, found := strings.CutPrefix(k, xattrPrefix)
		if !found || name == "" {
			continue
		}
		if !keepXattr(name) {
			continue
		}
		if out == nil {
			out = make(map[string]string, 1)
		}
		out[name] = v
	}
	return out
}

func keepXattr(name string) bool {
	if name == "security.capability" {
		return true
	}
	return !strings.HasPrefix(name, "security.") && !strings.HasPrefix(name, "system.")
}
