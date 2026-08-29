package analyze

import (
	"path"
	"strings"

	"github.com/ericsuh/layerlens/internal/domain"
)

// implicitDirMode is the mode given to a directory that exists only because a
// child needed a parent. It is synthetic — never a real header value — which
// is why an implicit directory must never register as a modification (§4.2).
const implicitDirMode uint32 = 0o755

// Squash folds layer changesets 1..N into the cumulative filesystem tree at
// layer point N (ARCHITECTURE §4.2). indexes are in rootfs order (oldest
// first); an empty slice yields an empty root.
//
// Each layer is applied in two passes, and that structure is the whole
// correctness argument:
//
//  1. deletions (opaque markers, then whiteouts) apply to the *lower* state
//     only — everything the previous layers built;
//  2. the layer's own entries are then upserted.
//
// The OCI image-spec requires opaque markers to be "applied first, regardless
// of the ordering in which the whiteout file was encountered", and whiteouts
// to apply only to lower layers. Because the two passes are separate loops
// over the same changeset, that holds no matter where the markers sit in tar
// order — the classic reader bug (a single ordered pass that happens to work
// because `.wh.` sorts early) is impossible here by construction.
//
// The returned tree owns its nodes; the caller may mutate or drop it freely.
// Whiteout and opaque entries are consumed here and never reach a Node.
func Squash(indexes []domain.LayerIndex) *domain.Node {
	root := newDir(RootPath)
	// The root is synthetic: no layer can carry an entry for "/" (the
	// indexer drops root-named members), so its 0755 must never read as a
	// difference between two images.
	root.Implicit = true

	for i := range indexes {
		applyLayer(root, indexes[i].Entries)
	}
	return root
}

// applyLayer applies one layer's changeset to the cumulative tree.
func applyLayer(root *domain.Node, entries []domain.Entry) {
	// Pass 1a — opaque markers. The directory node itself survives; only
	// the lower layers' children beneath it are hidden. A marker for a
	// directory that does not exist below (or that is not a directory
	// below) is a no-op: if this layer also ships the directory, its own
	// entry lands in pass 2 and starts from an empty child map anyway.
	for i := range entries {
		e := &entries[i]
		if e.Kind != domain.KindOpaque {
			continue
		}
		if d := lookup(root, e.Path); d != nil && d.Kind == domain.KindDir {
			d.Children = make(map[string]*domain.Node)
		}
	}

	// Pass 1b — explicit whiteouts. Both marker kinds only ever delete, so
	// their relative order is irrelevant: an opaque marker and an explicit
	// whiteout in the same directory and layer compose to the same lower
	// state either way.
	for i := range entries {
		e := &entries[i]
		if e.Kind != domain.KindWhiteout {
			continue
		}
		// A whiteout for a path absent below is a no-op, not an error.
		removeSubtree(root, e.Path)
	}

	// Pass 2 — the layer's own entries. A layer that whiteouts x and also
	// ships x therefore keeps its own x: the deletion hit the lower state
	// in pass 1, and the changeset legitimately records both changes.
	for i := range entries {
		e := &entries[i]
		if e.Kind == domain.KindWhiteout || e.Kind == domain.KindOpaque {
			continue
		}
		upsert(root, e)
	}
}

// upsert inserts or replaces one entry, creating implicit parents as needed.
func upsert(root *domain.Node, e *domain.Entry) {
	dir, name := path.Split(e.Path)
	if name == "" {
		// Defensive: sanitized paths never end in "/", so this cannot
		// happen for indexer output. Ignoring it beats corrupting the
		// tree with an unnamed node.
		return
	}
	parent := ensureDirs(root, dir)

	n := nodeFrom(e, name)
	if e.Kind == domain.KindDir {
		if old := parent.Children[name]; old != nil && old.Kind == domain.KindDir {
			// Re-stating a directory updates its metadata and KEEPS
			// its children (and, because nodeFrom leaves Implicit
			// false, an explicit entry clears the implicit flag).
			// If an opaque marker already cleared the map in pass 1,
			// this inherits the cleared map and resurrects nothing.
			n.Children = old.Children
		} else {
			// A directory replacing a non-directory starts empty.
			n.Children = make(map[string]*domain.Node)
		}
	}
	// A non-directory replacing a directory drops the old subtree
	// wholesale: the type change hides everything below it, which is what
	// an overlay mount shows.
	parent.Children[name] = n
}

// ensureDirs walks (and creates) the directory chain for a "/"-rooted
// directory path, returning the node the caller should write into. Missing
// components become implicit directories: layer tars routinely omit the
// headers for parent directories.
func ensureDirs(root *domain.Node, dir string) *domain.Node {
	cur := root
	for _, seg := range segments(dir) {
		child := cur.Children[seg]
		if child == nil || child.Kind != domain.KindDir {
			// Either nothing is here, or something that cannot hold
			// children is (a file whose "subdirectory" a later entry
			// claims). Both cases become a fresh implicit directory;
			// the shadowed node is gone, exactly as with any other
			// type change.
			child = newDir(seg)
			child.Implicit = true
			cur.Children[seg] = child
		}
		cur = child
	}
	return cur
}

// lookup returns the node at a "/"-rooted path, or nil if absent. The root
// itself is addressable as "/" — that is how an opaque marker at the top level
// arrives.
func lookup(root *domain.Node, p string) *domain.Node {
	cur := root
	for _, seg := range segments(p) {
		if cur.Children == nil {
			return nil
		}
		next := cur.Children[seg]
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

// removeSubtree deletes the node at p and everything beneath it. Deleting the
// root is refused: no whiteout can name it, and silently emptying the tree
// would be the worst possible interpretation of a malformed marker.
func removeSubtree(root *domain.Node, p string) {
	segs := segments(p)
	if len(segs) == 0 {
		return
	}
	parent := lookup(root, "/"+strings.Join(segs[:len(segs)-1], "/"))
	if parent == nil || parent.Children == nil {
		return
	}
	delete(parent.Children, segs[len(segs)-1])
}

// segments splits a "/"-rooted path into its non-empty components.
func segments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// newDir builds an explicit, empty directory node.
func newDir(name string) *domain.Node {
	return &domain.Node{
		Name:     name,
		Kind:     domain.KindDir,
		Mode:     implicitDirMode,
		Children: make(map[string]*domain.Node),
	}
}

// nodeFrom projects a changeset entry onto a tree node. Children are left nil;
// upsert attaches the right child map for directories.
//
// Hardlinks keep their KindHardlink and LinkTarget and carry Size 0, so their
// bytes are counted once — at the target. The link is never resolved, so a
// link whose target a later layer whiteouts is displayed as it is, dangling,
// rather than silently repaired.
func nodeFrom(e *domain.Entry, name string) *domain.Node {
	return &domain.Node{
		Name:       name,
		Kind:       e.Kind,
		Mode:       e.Mode & domain.ModePermMask,
		Size:       e.Size,
		UID:        e.UID,
		GID:        e.GID,
		Uname:      e.Uname,
		Gname:      e.Gname,
		Devmajor:   e.Devmajor,
		Devminor:   e.Devminor,
		Xattrs:     e.Xattrs,
		MtimeUnix:  e.MtimeUnix,
		LinkTarget: e.LinkTarget,
		ContentSHA: e.ContentSHA,
	}
}
