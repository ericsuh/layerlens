package domain

// EntryKind classifies a changeset entry. The two whiteout kinds are markers
// rather than filesystem objects: squashing consumes them and they never reach
// a Node (§3.2).
type EntryKind uint8

// Entry kinds. The numeric values are part of the on-disk index format and of
// the changeset digest, so they must never be renumbered.
const (
	KindFile EntryKind = iota
	KindDir
	KindSymlink
	KindHardlink
	// KindDevice collapses block and character devices; Size is 0 and the
	// device numbers live in Devmajor/Devminor.
	KindDevice
	KindFifo
	// KindWhiteout is ".wh.<name>"; Path is the *target* path being deleted.
	KindWhiteout
	// KindOpaque is ".wh..wh..opq"; Path is the directory being made opaque.
	KindOpaque
)

// String renders a kind for diagnostics and warnings.
func (k EntryKind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindSymlink:
		return "symlink"
	case KindHardlink:
		return "hardlink"
	case KindDevice:
		return "device"
	case KindFifo:
		return "fifo"
	case KindWhiteout:
		return "whiteout"
	case KindOpaque:
		return "opaque"
	default:
		return "unknown"
	}
}

// ModePermMask selects the bits of a tar mode that layerlens keeps: the nine
// permission bits plus setuid, setgid and sticky.
const ModePermMask uint32 = 0o7777

// Entry is one path in a single layer's changeset.
//
// Every field except MtimeUnix participates in the normalized changeset digest
// (§3.1, RESEARCH Q9): the field set is tarsum v1, which is what Docker's
// *build* cache hashes. mtime is the only exclusion — it is exactly the thing
// that makes two byte-identical rebuilds produce different DiffIDs, which is
// the phenomenon layerlens exists to expose.
//
// Keep this struct compact: a pathological 500k-entry layer holds one of these
// per path, at a budget of roughly 200 B each (§4.6).
type Entry struct {
	// Path is cleaned and "/"-rooted, with no trailing slash
	// ("/usr/lib/x.so"). For whiteouts it is the target path; for opaque
	// markers it is the directory.
	Path string    `json:"path"`
	Kind EntryKind `json:"kind,omitempty"`
	// Mode holds the lower 12 bits of the tar mode (ModePermMask).
	Mode uint32 `json:"mode,omitempty"`
	UID  int    `json:"uid,omitempty"`
	GID  int    `json:"gid,omitempty"`
	// Uname and Gname are the tar uname/gname records.
	Uname string `json:"uname,omitempty"`
	Gname string `json:"gname,omitempty"`
	// Devmajor and Devminor are meaningful for KindDevice only.
	Devmajor int64 `json:"devmajor,omitempty"`
	Devminor int64 `json:"devminor,omitempty"`
	// Xattrs are the SCHILY.xattr.* PAX records that survived BuildKit's
	// filter (keep security.capability, drop other security.* and
	// system.*). Sorted by key when hashed.
	Xattrs map[string]string `json:"xattrs,omitempty"`
	// Size is set for regular files only; hardlinks carry 0 because their
	// content is counted once, at the link target.
	Size int64 `json:"size,omitempty"`
	// MtimeUnix is stored for display and is the ONLY field excluded from
	// the changeset digest.
	MtimeUnix int64 `json:"mtime,omitempty"`
	// LinkTarget is a symlink target verbatim, or a hardlink target path
	// (cleaned). It is never dereferenced on the server filesystem.
	LinkTarget string `json:"linkTarget,omitempty"`
	// ContentSHA is set for regular files only, hashed during the single
	// streaming pass over the layer.
	ContentSHA Digest `json:"contentSha,omitempty"`
}

// LayerIndexSchemaVersion is the major version of the per-layer index format.
// It is written into every serialized index and into LayerIndex.SchemaVersion;
// readers reject anything else (ARCHITECTURE §5).
const LayerIndexSchemaVersion = 1

// LayerIndex is the complete changeset of one layer: everything layerlens
// retains about a layer after streaming it once. The layer blob itself is
// never stored.
type LayerIndex struct {
	// SchemaVersion is the index format version (index.SchemaVersion).
	SchemaVersion int `json:"schemaVersion"`
	// DiffID is the verified digest of the uncompressed layer tar.
	DiffID Digest `json:"diffId"`
	// ChangesetDigest is the normalized (tarsum-v1 field set) digest.
	ChangesetDigest Digest `json:"changesetDigest"`
	// ContentBytes is the sum of regular-file sizes in this changeset.
	ContentBytes int64 `json:"contentBytes"`
	// Entries are sorted by Path with exactly one entry per path
	// (last-in-tar wins).
	Entries []Entry `json:"entries"`
	// Warnings records tar entries that were skipped, e.g. names that
	// failed sanitization.
	Warnings []string `json:"warnings,omitempty"`
}
