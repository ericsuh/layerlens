package analyze

import (
	"sort"

	"github.com/ericsuh/layerlens/internal/domain"
)

// Diff compares two cumulative trees into one unified diff tree with
// bottom-up aggregates (ARCHITECTURE §4.3, §4.4). Either side may be nil,
// which marks the other side's whole subtree added or removed.
//
// The result is self-contained: every per-side value is copied into a
// SideMeta, and no *Node pointer is retained. Both input trees are therefore
// garbage the moment Diff returns, which is what keeps the §4.6 memory budget
// ("side trees are transient") true rather than aspirational.
//
// Aggregates are filled during the same post-order visit, so there is no
// second pass over the merged tree.
func Diff(l, r *domain.Node) *domain.DiffNode {
	switch {
	case l == nil && r == nil:
		return nil
	case l == nil:
		return markSubtree(r, domain.StatusAdded)
	case r == nil:
		return markSubtree(l, domain.StatusRemoved)
	}

	d := &domain.DiffNode{
		Name:  l.Name,
		Left:  metaOf(l),
		Right: metaOf(r),
	}

	if l.Kind == domain.KindDir && r.Kind == domain.KindDir {
		names := sortedUnion(l.Children, r.Children)
		d.Children = make([]*domain.DiffNode, 0, len(names))
		changedChild := false
		for _, name := range names {
			c := Diff(l.Children[name], r.Children[name])
			if c == nil {
				continue
			}
			if c.Status != domain.StatusUnchanged {
				changedChild = true
			}
			d.Children = append(d.Children, c)
		}
		SortDiffChildren(d.Children)

		if changedChild || metaDiffers(l, r) {
			d.Status = domain.StatusModified
		} else {
			d.Status = domain.StatusUnchanged
		}
	} else {
		// A path that is a directory on one side and something else on
		// the other is a leaf here: the vanished subtree is not part of
		// the comparison, matching §4.3 and the overlay behaviour that
		// a type change hides everything below it.
		//
		// Its bytes are therefore absent from this row's Agg and from
		// every ancestor's — the documented dir↔file exception in
		// §6.5. Counting them into LeftBytes/LeftFiles instead would
		// put bytes into a total that no reachable row accounts for and
		// break §4.4's `Agg == Σ children.Agg`, which is what lets a
		// client reconcile a parent against the page it just expanded.
		// A client detects the case from the wire — status "modified"
		// with left.kind "dir" and a right.kind that is not — and
		// labels the row rather than silently under-reporting.
		if metaDiffers(l, r) {
			d.Status = domain.StatusModified
		} else {
			d.Status = domain.StatusUnchanged
		}
	}

	fillAgg(d)
	return d
}

// metaDiffers is the modification predicate for a path present on both sides:
// the tarsum-v1 field set (§3.2), which is literally the same projection the
// changeset digest hashes — mtime is the only difference that does not make a
// path modified.
//
// The one exception is the implicit directory. Its mode (and uid/gid) are
// synthetic, invented because a child needed a parent, so comparing them would
// report modifications that exist nowhere but in our own bookkeeping. A kind
// change is still a change: only the metadata comparison is skipped, and only
// when both sides are directories.
func metaDiffers(l, r *domain.Node) bool {
	if l.Kind != r.Kind {
		return true
	}
	if l.Kind == domain.KindDir && (l.Implicit || r.Implicit) {
		return false
	}
	return !fieldsOfNode(l).equal(fieldsOfNode(r))
}

// markSubtree builds a one-sided diff subtree, filling Agg on the way down —
// there is no second pass that could fix it up later.
func markSubtree(n *domain.Node, status domain.DiffStatus) *domain.DiffNode {
	d := &domain.DiffNode{Name: n.Name, Status: status}
	meta := metaOf(n)
	if status == domain.StatusAdded {
		d.Right = meta
	} else {
		d.Left = meta
	}

	if len(n.Children) > 0 {
		d.Children = make([]*domain.DiffNode, 0, len(n.Children))
		for _, c := range n.Children {
			d.Children = append(d.Children, markSubtree(c, status))
		}
		SortDiffChildren(d.Children)
	}

	fillAgg(d)
	return d
}

// metaOf copies a node's per-side metadata. The Xattrs map is shared with the
// node, which is safe because neither the tree nor the diff is mutated after
// construction, and it keeps a per-path map allocation off the budget.
func metaOf(n *domain.Node) *domain.SideMeta {
	return &domain.SideMeta{
		Kind:       n.Kind,
		Mode:       n.Mode & domain.ModePermMask,
		Size:       n.Size,
		UID:        n.UID,
		GID:        n.GID,
		Uname:      n.Uname,
		Gname:      n.Gname,
		Devmajor:   n.Devmajor,
		Devminor:   n.Devminor,
		Xattrs:     n.Xattrs,
		LinkTarget: n.LinkTarget,
		ContentSHA: n.ContentSHA,
		Implicit:   n.Implicit,
	}
}

// fillAgg computes a node's aggregate as its own contribution plus the sum of
// its children's (ARCHITECTURE §4.4). Children are already final: this runs at
// the end of the post-order visit.
//
// Only regular files contribute bytes and counts. Directories contribute
// nothing of their own — so for a directory the invariant is exactly
// `Agg == Σ children.Agg` — and symlinks, hardlinks, devices and fifos
// contribute zero bytes and are excluded from the file counts while still
// appearing as rows with their own status. Hardlinks carry Size 0 from the
// indexer, so a file's bytes are counted once, at the link target.
func fillAgg(d *domain.DiffNode) {
	a := &d.Agg

	leftFile := d.Left != nil && d.Left.Kind == domain.KindFile
	rightFile := d.Right != nil && d.Right.Kind == domain.KindFile

	if leftFile {
		a.LeftBytes += d.Left.Size
		a.LeftFiles++
	}
	if rightFile {
		a.RightBytes += d.Right.Size
		a.RightFiles++
	}

	switch d.Status {
	case domain.StatusAdded:
		if rightFile {
			a.AddedBytes += d.Right.Size
			a.AddedFiles++
		}
	case domain.StatusRemoved:
		if leftFile {
			a.RemovedBytes += d.Left.Size
			a.RemovedFiles++
		}
	case domain.StatusModified:
		// A modified file counts on BOTH sides' byte totals: "how big
		// was it" and "how big is it" are different questions and the
		// UI shows both.
		if leftFile {
			a.ModifiedBytesLeft += d.Left.Size
		}
		if rightFile {
			a.ModifiedBytesRight += d.Right.Size
		}
		if leftFile || rightFile {
			a.ModifiedFiles++
		}
	case domain.StatusUnchanged:
	}

	for _, c := range d.Children {
		addAgg(a, &c.Agg)
	}
}

func addAgg(dst *domain.Agg, src *domain.Agg) {
	dst.LeftBytes += src.LeftBytes
	dst.RightBytes += src.RightBytes
	dst.LeftFiles += src.LeftFiles
	dst.RightFiles += src.RightFiles
	dst.AddedBytes += src.AddedBytes
	dst.RemovedBytes += src.RemovedBytes
	dst.ModifiedBytesLeft += src.ModifiedBytesLeft
	dst.ModifiedBytesRight += src.ModifiedBytesRight
	dst.AddedFiles += src.AddedFiles
	dst.RemovedFiles += src.RemovedFiles
	dst.ModifiedFiles += src.ModifiedFiles
}

// sortedUnion returns every child name present in either map, sorted, once
// each.
func sortedUnion(l, r map[string]*domain.Node) []string {
	names := make([]string, 0, len(l)+len(r))
	for name := range l {
		names = append(names, name)
	}
	for name := range r {
		if _, both := l[name]; !both {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
