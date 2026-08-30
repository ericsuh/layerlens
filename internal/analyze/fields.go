package analyze

import (
	"hash"
	"sort"

	"github.com/ericsuh/layerlens/internal/domain"
)

// tarsumFields is the tarsum-v1 field selection of one path (ARCHITECTURE
// §3.1/§3.2, RESEARCH Q9): everything Docker/BuildKit hashes for its build
// cache, which is every field of a tar header *except* mtime.
//
// This type is the single source of truth for "are these two paths the same?".
// Both the normalized changeset digest (which decides whether two layers get a
// dotted could-be-shared edge) and the diff tree's modification predicate are
// expressed in terms of it, so the two can never disagree about what "same"
// means — the §3.2 guarantee is structural rather than a promise kept by two
// parallel field lists.
type tarsumFields struct {
	Kind       domain.EntryKind
	Mode       uint32
	UID, GID   int
	Uname      string
	Gname      string
	Size       int64
	LinkTarget string
	Devmajor   int64
	Devminor   int64
	Xattrs     map[string]string
	ContentSHA domain.Digest
	// MtimeUnix is deliberately absent: mtime churn is exactly what breaks
	// DiffID equality between two otherwise identical rebuilds, and it is
	// the one difference layerlens must not call a change.
}

// fieldsOfEntry projects a changeset entry onto the field set. It normalizes
// nothing beyond the mode mask and the directory projection below: the entry is
// otherwise what the tar said.
func fieldsOfEntry(e *domain.Entry) tarsumFields {
	f := tarsumFields{
		Kind:       e.Kind,
		Mode:       e.Mode & domain.ModePermMask,
		UID:        e.UID,
		GID:        e.GID,
		Uname:      e.Uname,
		Gname:      e.Gname,
		Size:       e.Size,
		LinkTarget: e.LinkTarget,
		Devmajor:   e.Devmajor,
		Devminor:   e.Devminor,
		Xattrs:     e.Xattrs,
		ContentSHA: e.ContentSHA,
	}
	projectDir(&f)
	return f
}

// projectDir applies the one normalization both projections must share: a
// directory has neither a size nor a content hash, and the tar `size` field of
// a directory member is meaningless. Applying it in exactly one place is what
// makes §3.2's "the digest and the modified predicate cannot disagree"
// structural rather than a promise kept by two parallel field lists.
func projectDir(f *tarsumFields) {
	if f.Kind != domain.KindDir {
		return
	}
	f.Size = 0
	f.ContentSHA = ""
}

// fieldsOfNode projects a cumulative-tree node onto the same field set,
// through the same directory projection, so a stray header value can never
// read as a directory "modification" on one side of the guarantee and as a
// digest difference on the other (ARCHITECTURE §3.2, §4.3).
func fieldsOfNode(n *domain.Node) tarsumFields {
	f := tarsumFields{
		Kind:       n.Kind,
		Mode:       n.Mode & domain.ModePermMask,
		UID:        n.UID,
		GID:        n.GID,
		Uname:      n.Uname,
		Gname:      n.Gname,
		Size:       n.Size,
		LinkTarget: n.LinkTarget,
		Devmajor:   n.Devmajor,
		Devminor:   n.Devminor,
		Xattrs:     n.Xattrs,
		ContentSHA: n.ContentSHA,
	}
	projectDir(&f)
	return f
}

// equal reports whether two paths are the same under the tarsum-v1 rule.
func (f tarsumFields) equal(g tarsumFields) bool {
	if f.Kind != g.Kind ||
		f.Mode != g.Mode ||
		f.UID != g.UID ||
		f.GID != g.GID ||
		f.Uname != g.Uname ||
		f.Gname != g.Gname ||
		f.Size != g.Size ||
		f.LinkTarget != g.LinkTarget ||
		f.Devmajor != g.Devmajor ||
		f.Devminor != g.Devminor ||
		f.ContentSHA != g.ContentSHA {
		return false
	}
	return xattrsEqual(f.Xattrs, g.Xattrs)
}

func xattrsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}

// writeFields appends one path's field selection to h. Every field is length-
// or width-prefixed, so no combination of values can be re-parsed as a
// different combination.
func writeFields(h hash.Hash, path string, f tarsumFields) {
	writeStr(h, path)
	writeUint(h, uint64(f.Kind))
	writeUint(h, uint64(f.Mode))
	writeInt(h, int64(f.UID))
	writeInt(h, int64(f.GID))
	writeStr(h, f.Uname)
	writeStr(h, f.Gname)
	writeInt(h, f.Size)
	writeStr(h, f.LinkTarget)
	writeInt(h, f.Devmajor)
	writeInt(h, f.Devminor)

	keys := make([]string, 0, len(f.Xattrs))
	for k := range f.Xattrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeUint(h, uint64(len(keys)))
	for _, k := range keys {
		writeStr(h, k)
		writeStr(h, f.Xattrs[k])
	}

	// Regular files only; empty for every other kind.
	writeStr(h, string(f.ContentSHA))
}
