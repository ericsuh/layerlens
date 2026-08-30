package domain

// Node is one path in the cumulative (squashed) filesystem at a layer point
// (ARCHITECTURE §3.2). Whiteout and opaque entries are consumed by squashing
// and never appear here.
type Node struct {
	Name       string
	Kind       EntryKind
	Mode       uint32
	Size       int64
	UID, GID   int
	Uname      string
	Gname      string
	Devmajor   int64
	Devminor   int64
	Xattrs     map[string]string
	MtimeUnix  int64
	LinkTarget string
	ContentSHA Digest
	// Implicit is true for a directory that exists only because a child
	// needed a parent (§4.2). Implicit directories never flag a diff.
	Implicit bool
	// Children is non-nil for directories only.
	Children map[string]*Node
}

// DiffStatus is the per-path verdict of a two-tree comparison.
type DiffStatus uint8

// Diff statuses.
const (
	StatusUnchanged DiffStatus = iota
	StatusAdded
	StatusRemoved
	StatusModified
)

// String renders a status for JSON DTOs and diagnostics.
func (s DiffStatus) String() string {
	switch s {
	case StatusUnchanged:
		return "unchanged"
	case StatusAdded:
		return "added"
	case StatusRemoved:
		return "removed"
	case StatusModified:
		return "modified"
	default:
		return "unknown"
	}
}

// SideMeta is the per-side metadata copied out of a Node so that a DiffNode is
// self-contained: the side trees are released once the diff is built.
//
// The fields are exactly the tarsum-v1 field set, so the "modified" predicate
// and the changeset digest can never disagree about what "same" means (§3.2).
type SideMeta struct {
	Kind       EntryKind
	Mode       uint32
	Size       int64
	UID, GID   int
	Uname      string
	Gname      string
	Devmajor   int64
	Devminor   int64
	Xattrs     map[string]string
	LinkTarget string
	ContentSHA Digest
	// Implicit mirrors Node.Implicit: this directory was never named by
	// any layer header and its mode, uid and gid are values squashing
	// invented so a child could have a parent. It is carried onto the wire
	// so a client renders "—" rather than a fabricated 0755 (§4.2).
	//
	// It is deliberately NOT part of the tarsum-v1 field set: it says
	// where the metadata came from, not what it is.
	Implicit bool
}

// Agg is the bottom-up aggregation over a diff subtree (§4.4). The invariant
// is parent.Agg == Σ children.Agg for directories.
type Agg struct {
	LeftBytes, RightBytes int64
	LeftFiles, RightFiles int64

	AddedBytes, RemovedBytes                int64
	ModifiedBytesLeft, ModifiedBytesRight   int64
	AddedFiles, RemovedFiles, ModifiedFiles int64
}

// DiffNode is one path in the unified comparison tree.
type DiffNode struct {
	Name string
	// Status of a directory is Modified iff any descendant or its own
	// metadata changed.
	Status DiffStatus
	// Left is nil when the path was added on the right.
	Left *SideMeta
	// Right is nil when the path was removed on the right.
	Right *SideMeta
	// Agg is filled bottom-up; for files it is degenerate (own size/count).
	Agg Agg
	// Children are sorted directories-first, then files, each by name.
	Children []*DiffNode
}
