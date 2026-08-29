// Package gen builds the vendored OCI image-layout fixtures that layerlens
// demos and tests against (ARCHITECTURE §9.2, RESEARCH Q2).
//
// Everything here is deterministic by construction: entry specs are Go
// literals, tar members are emitted in sorted order with fixed timestamps and
// ownership, file bodies are a pure function of a seed, and gzip is written
// with a zeroed header. Running the generator twice produces byte-identical
// trees, which is what makes `mise run genfixtures` reviewable as a no-op diff.
//
// The one deliberate exception is mtime skew: the two `RUN npm install` layers
// of the golden pair ship identical files with different modification times.
// That is the single most load-bearing property of the whole fixture set — it
// is what makes their DiffIDs differ while their normalized changeset digests
// stay equal, i.e. what produces the dotted "could be shared" edge the demo
// exists to explain.
//
// The generator is a development tool, so the internal/ import rules of
// ARCHITECTURE §2 do not bind it; it nevertheless depends only on the standard
// library and internal/domain.
package gen

import (
	"time"

	"github.com/ericsuh/layerlens/internal/domain"
)

// Platform of every fixture image. Multiplatform images are out of scope
// (PROJECT.md "Out of scope").
const (
	Architecture = "amd64"
	OS           = "linux"
)

// RefNameAnnotation is how a fixture layout advertises the display reference
// of each manifest in its index.json. Phase 005 discovers tags through this
// annotation, so the convention is fixed here:
//
//	"org.opencontainers.image.ref.name": "example:v1"
//
// It is the annotation `oras`, `skopeo` and `crane` all write for a tagged
// manifest in an OCI layout, so the fixtures stay readable by standard tooling.
const RefNameAnnotation = "org.opencontainers.image.ref.name"

// OCI media types written into the fixture layouts.
const (
	MediaTypeIndex    = "application/vnd.oci.image.index.v1+json"
	MediaTypeManifest = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeConfig   = "application/vnd.oci.image.config.v1+json"
	MediaTypeLayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
)

// EntrySpec describes one tar member of one layer. It is the reviewable unit
// of a fixture: every byte the generator emits is derived from these literals.
type EntrySpec struct {
	// Path is the "/"-rooted path of the filesystem object. For a whiteout
	// it is the path being deleted; for an opaque marker it is the
	// directory being emptied. The generator derives the on-tar member
	// name (including the ".wh." decorations) from it.
	Path string
	// Kind selects the tar typeflag.
	Kind domain.EntryKind
	// Size is the body length of a regular file.
	Size int64
	// Seed selects the file body. It defaults to Path; set it explicitly
	// when the same path must carry different content in two images (the
	// modified /app/main.js of the golden pair) or the same content under
	// two different paths.
	Seed string
	// Mode holds the permission bits; zero means "use the default for this
	// kind" (0644 files, 0755 dirs, 0777 symlinks, 0600 devices/fifos).
	Mode uint32
	// UID/GID and Uname/Gname participate in the changeset digest
	// (RESEARCH Q9), so they are explicit rather than incidental.
	UID, GID     int
	Uname, Gname string
	// Link is the symlink or hardlink target.
	Link string
	// Devmajor/Devminor apply to device nodes.
	Devmajor, Devminor int64
	// Xattrs are stored as SCHILY.xattr.* PAX records.
	Xattrs map[string]string
	// Mtime overrides the layer's default modification time. Layer-level
	// defaults cover almost everything; this exists for the few entries
	// whose timestamps carry meaning.
	Mtime time.Time
}

// EntryOpt customizes an EntrySpec built by one of the constructors below.
type EntryOpt func(*EntrySpec)

// WithMode sets the permission bits.
func WithMode(m uint32) EntryOpt { return func(e *EntrySpec) { e.Mode = m } }

// WithOwner sets the numeric uid and gid.
func WithOwner(uid, gid int) EntryOpt {
	return func(e *EntrySpec) { e.UID, e.GID = uid, gid }
}

// WithNames sets the uname and gname records.
func WithNames(uname, gname string) EntryOpt {
	return func(e *EntrySpec) { e.Uname, e.Gname = uname, gname }
}

// WithSeed overrides the content seed, decoupling a file's bytes from its path.
func WithSeed(seed string) EntryOpt { return func(e *EntrySpec) { e.Seed = seed } }

// WithMtime overrides the layer's default modification time for one entry.
func WithMtime(t time.Time) EntryOpt { return func(e *EntrySpec) { e.Mtime = t } }

// WithXattr adds one extended attribute.
func WithXattr(name, value string) EntryOpt {
	return func(e *EntrySpec) {
		if e.Xattrs == nil {
			e.Xattrs = map[string]string{}
		}
		e.Xattrs[name] = value
	}
}

func newEntry(path string, kind domain.EntryKind, opts []EntryOpt) EntrySpec {
	e := EntrySpec{Path: path, Kind: kind}
	for _, o := range opts {
		o(&e)
	}
	return e
}

// File declares a regular file of the given size.
func File(path string, size int64, opts ...EntryOpt) EntrySpec {
	e := newEntry(path, domain.KindFile, opts)
	e.Size = size
	return e
}

// Dir declares a directory.
func Dir(path string, opts ...EntryOpt) EntrySpec {
	return newEntry(path, domain.KindDir, opts)
}

// Symlink declares a symbolic link. The target is stored verbatim.
func Symlink(path, target string, opts ...EntryOpt) EntrySpec {
	e := newEntry(path, domain.KindSymlink, opts)
	e.Link = target
	return e
}

// Hardlink declares a hard link to an earlier member of the same tar.
func Hardlink(path, target string, opts ...EntryOpt) EntrySpec {
	e := newEntry(path, domain.KindHardlink, opts)
	e.Link = target
	return e
}

// Device declares a character (char true) or block device node.
func Device(path string, char bool, major, minor int64, opts ...EntryOpt) EntrySpec {
	e := newEntry(path, domain.KindDevice, opts)
	e.Devmajor, e.Devminor = major, minor
	// Kind alone cannot distinguish char from block, so the typeflag is
	// carried in Seed, which is meaningless for devices otherwise.
	if char {
		e.Seed = charDeviceSeed
	}
	return e
}

// charDeviceSeed marks a Device spec as a character rather than block device.
const charDeviceSeed = "\x00char"

// Fifo declares a named pipe.
func Fifo(path string, opts ...EntryOpt) EntrySpec {
	return newEntry(path, domain.KindFifo, opts)
}

// Whiteout declares a ".wh."-prefixed marker deleting path from lower layers.
func Whiteout(path string) EntrySpec {
	return EntrySpec{Path: path, Kind: domain.KindWhiteout}
}

// Opaque declares a ".wh..wh..opq" marker hiding every lower-layer child of
// dir.
func Opaque(dir string) EntrySpec {
	return EntrySpec{Path: dir, Kind: domain.KindOpaque}
}

// LayerSpec is one step of a fixture Dockerfile: either a layer with a
// changeset, or a history entry that consumes no layer at all.
type LayerSpec struct {
	// CreatedBy is the verbatim config history `created_by` string. It is
	// what analyze.MapHistory maps onto rootfs.diff_ids and what the UI
	// cleans for display.
	CreatedBy string
	// Empty marks a history entry with empty_layer: true (ENV, CMD,
	// LABEL, ...). Such a step contributes no diff_id and its Entries are
	// ignored.
	Empty bool
	// Mtime is the default modification time of this layer's entries.
	Mtime time.Time
	// Entries are the layer's tar members, in any order: the writer sorts
	// them by member name so the output cannot depend on the literal's
	// ordering.
	Entries []EntrySpec
}

// ImageSpec is one fixture image.
type ImageSpec struct {
	// Ref is the display reference written into the index.json ref-name
	// annotation, e.g. "example:v1".
	Ref string
	// Created is the image config's `created` timestamp and the timestamp
	// of every history entry.
	Created time.Time
	// Env, Entrypoint, Cmd, WorkingDir and Labels populate the runtime
	// config. They are display-only for layerlens but make the fixtures
	// look like real images to any other tool that reads them.
	Env        []string
	Entrypoint []string
	Cmd        []string
	WorkingDir string
	Labels     map[string]string
	// Layers are the build steps, oldest first.
	Layers []LayerSpec
}

// PairSpec is one OCI layout directory holding a comparable pair of images.
type PairSpec struct {
	// Name is the directory under the output root, e.g. "example".
	Name string
	// Doc explains what property of the analyzer the pair exercises. It is
	// printed by the CLI so a reviewer can see the intent without reading
	// the Go literals.
	Doc string
	// Images are the two (or more) images sharing this layout's blob store.
	Images []ImageSpec
}
