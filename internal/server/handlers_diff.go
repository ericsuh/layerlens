package server

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// Tree pagination bounds (§6.5). The maximum is what keeps a single response
// bounded no matter what the client asks for; the default is what keeps the
// common case around 50–70 KB.
const (
	DefaultTreeLimit = 200
	MaxTreeLimit     = 1000
	DefaultTreeDepth = 1
	MaxTreeDepth     = 2
)

// Tree filters.
const (
	FilterAll     = "all"
	FilterChanged = "changed"
)

// handleDiffLayers serves GET /api/v1/diff/layers (§6.4).
func (s *Server) handleDiffLayers(w http.ResponseWriter, r *http.Request) {
	left, right, ok := s.loadPair(w, r)
	if !ok {
		return
	}

	// The trunk is the longest common prefix of the two DiffID lists, and
	// nothing else: it is the only comparison that answers "do these images
	// actually share cached layers?" (DECISIONS A4).
	k := analyze.TrunkLCP(left.Layers, right.Layers)
	graph := LayerGraph{
		Left:        summaryOf(left),
		Right:       summaryOf(right),
		TrunkLength: k,
		Trunk:       graphLayersOf(left.Layers[:k], OwnerShared),
		LeftBranch:  graphLayersOf(left.Layers[k:], OwnerLeft),
		RightBranch: graphLayersOf(right.Layers[k:], OwnerRight),
		// A changeset match is reported here and only here. It is a
		// separate field from Owner precisely so that the wire format
		// cannot be read as claiming a cache hit (§3, §4.5).
		CouldBeShared: edgesOf(analyze.CouldBeSharedEdges(left.Layers, right.Layers, k)),
		MaxLayerBytes: maxLayerBytes(left, right),
	}
	s.touch(r, left.ID, right.ID)
	s.writeJSON(w, graph)
}

func maxLayerBytes(images ...*domain.ImageRecord) int64 {
	var maximum int64
	for _, rec := range images {
		for i := range rec.Layers {
			if b := rec.Layers[i].ContentBytes; b > maximum {
				maximum = b
			}
		}
	}
	return maximum
}

// treeParams is the validated form of a /diff/tree query.
type treeParams struct {
	key    comparisonKey
	path   string
	depth  int
	limit  int
	filter string
	cursor string
}

// handleDiffTree serves GET /api/v1/diff/tree (§6.5).
//
// Everything expensive happens once per (pair, selection): the comparison is
// assembled behind a single-flighted LRU, and each request is then a walk to
// one directory plus a slice of its children. The client never receives a
// whole tree, and the server never serializes one.
func (s *Server) handleDiffTree(w http.ResponseWriter, r *http.Request) {
	left, right, ok := s.loadPair(w, r)
	if !ok {
		return
	}
	params, ok := parseTreeParams(w, r, left, right)
	if !ok {
		return
	}

	cmp, err := s.comparisons.get(r.Context(), params.key, func(context.Context) (*comparison, error) {
		// Deliberately detached from this request: one assembly serves
		// every waiter on the same key, so the client that happened to
		// arrive first must not be able to cancel work the others are
		// blocked on by navigating away. The work is bounded (one
		// comparison) and its result is cached for whoever asks next.
		return s.assembleComparison(context.WithoutCancel(r.Context()),
			left, right, params.key.leftLayers, params.key.rightLayers)
	})
	if err != nil {
		s.writeStoreError(w, r, err, left.ID)
		return
	}
	s.touch(r, left.ID, right.ID)

	node := findNode(cmp.root, params.path)
	if node == nil {
		badRequest(w, "path %q does not exist in this comparison", params.path)
		return
	}

	query := cursorQuery(params.key, params.path, params.filter)
	cur, err := decodeCursor(params.cursor, query)
	if err != nil {
		badRequest(w, "cursor is not valid for this request; refetch from the first page")
		return
	}

	rows := filterRows(node.Children, params.filter)
	page := TreePage{
		Path:            params.path,
		TotalRows:       len(rows),
		MaxSiblingBytes: maxSiblingBytes(rows),
		PathStatus:      node.Status.String(),
		PathAgg:         aggOf(&node.Agg),
		Rows:            []TreeRow{},
	}

	start := pageStart(rows, cur)
	end := min(start+params.limit, len(rows))
	for _, row := range rows[start:end] {
		page.Rows = append(page.Rows, treeRowOf(row, params.path, params.depth, params.limit, params.filter))
	}
	if end < len(rows) {
		page.NextCursor = encodeCursor(query, rows[end-1])
	}
	s.writeJSON(w, page)
}

// treeRowOf renders one row, including its grandchildren when depth is 2.
func treeRowOf(node *domain.DiffNode, parent string, depth, limit int, filter string) TreeRow {
	rowPath := joinPath(parent, node.Name)
	children := filterRows(node.Children, filter)
	row := TreeRow{
		Name:   node.Name,
		Path:   rowPath,
		Status: node.Status.String(),
		Left:   sideMetaOf(node.Left),
		Right:  sideMetaOf(node.Right),
		Agg:    aggOf(&node.Agg),
		// Post-filter, per §6.5: these numbers describe what an expand
		// would actually return, which is what "N of M shown" and
		// aria-setsize are computed from.
		HasChildren: len(children) > 0,
		ChildCount:  len(children),
	}
	if depth < 2 || len(children) == 0 {
		return row
	}
	embedded := min(limit, len(children))
	row.Children = make([]TreeRow, 0, embedded)
	for _, child := range children[:embedded] {
		// Grandchildren are rendered at depth 1: depth=2 means two
		// levels, and recursing further would make the payload's bound
		// depend on tree shape rather than on the request.
		row.Children = append(row.Children, treeRowOf(child, rowPath, 1, limit, filter))
	}
	row.ChildrenTruncated = len(children) > embedded
	return row
}

// filterRows applies filter=changed. A directory's status is Modified exactly
// when something in its subtree changed (§4.3), so "status is not unchanged"
// is precisely "this subtree contains a change" — no second traversal needed.
func filterRows(rows []*domain.DiffNode, filter string) []*domain.DiffNode {
	if filter != FilterChanged {
		return rows
	}
	out := make([]*domain.DiffNode, 0, len(rows))
	for _, row := range rows {
		if row.Status != domain.StatusUnchanged {
			out = append(out, row)
		}
	}
	return out
}

// maxSiblingBytes is the relative-size-bar denominator: the largest combined
// subtree size among ALL of the rows, not just the ones on this page, so the
// bars keep their scale as the user pages through a wide directory.
func maxSiblingBytes(rows []*domain.DiffNode) int64 {
	var maximum int64
	for _, row := range rows {
		if total := row.Agg.LeftBytes + row.Agg.RightBytes; total > maximum {
			maximum = total
		}
	}
	return maximum
}

// findNode walks the diff tree to an absolute path.
func findNode(root *domain.DiffNode, p string) *domain.DiffNode {
	if root == nil {
		return nil
	}
	node := root
	for _, segment := range strings.Split(strings.Trim(p, "/"), "/") {
		if segment == "" {
			continue
		}
		var next *domain.DiffNode
		for _, child := range node.Children {
			if child.Name == segment {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

func joinPath(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

// loadPair resolves the left= and right= image ids.
func (s *Server) loadPair(w http.ResponseWriter, r *http.Request) (*domain.ImageRecord, *domain.ImageRecord, bool) {
	query := r.URL.Query()
	leftID, ok := parseImageID(w, query.Get("left"), "left")
	if !ok {
		return nil, nil, false
	}
	rightID, ok := parseImageID(w, query.Get("right"), "right")
	if !ok {
		return nil, nil, false
	}
	left, err := s.images.Image(r.Context(), leftID)
	if err != nil {
		s.writeStoreError(w, r, err, leftID)
		return nil, nil, false
	}
	right, err := s.images.Image(r.Context(), rightID)
	if err != nil {
		s.writeStoreError(w, r, err, rightID)
		return nil, nil, false
	}
	return left, right, true
}

// parseTreeParams validates every /diff/tree parameter, writing the §6.1
// envelope itself on failure.
func parseTreeParams(w http.ResponseWriter, r *http.Request, left, right *domain.ImageRecord) (treeParams, bool) {
	query := r.URL.Query()
	params := treeParams{
		key:    comparisonKey{left: left.ID, right: right.ID},
		path:   "/",
		depth:  DefaultTreeDepth,
		limit:  DefaultTreeLimit,
		filter: FilterAll,
		cursor: query.Get("cursor"),
	}

	// A layer selection is a COUNT of layers, not an index: n means
	// "layers 1..n", and 0 is the legal empty filesystem before any layer
	// is applied.
	leftLayers, ok := parseLayerCount(w, query.Get("leftLayers"), "leftLayers", len(left.Layers))
	if !ok {
		return params, false
	}
	rightLayers, ok := parseLayerCount(w, query.Get("rightLayers"), "rightLayers", len(right.Layers))
	if !ok {
		return params, false
	}
	params.key.leftLayers = leftLayers
	params.key.rightLayers = rightLayers

	if raw := query.Get("path"); raw != "" {
		clean, ok := cleanTreePath(w, raw)
		if !ok {
			return params, false
		}
		params.path = clean
	}
	if raw := query.Get("depth"); raw != "" {
		depth, err := strconv.Atoi(raw)
		if err != nil || depth < 1 || depth > MaxTreeDepth {
			badRequest(w, "depth must be 1 or %d", MaxTreeDepth)
			return params, false
		}
		params.depth = depth
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > MaxTreeLimit {
			badRequest(w, "limit must be between 1 and %d", MaxTreeLimit)
			return params, false
		}
		params.limit = limit
	}
	if raw := query.Get("filter"); raw != "" {
		if raw != FilterAll && raw != FilterChanged {
			badRequest(w, "filter must be %q or %q", FilterAll, FilterChanged)
			return params, false
		}
		params.filter = raw
	}
	return params, true
}

// parseLayerCount validates a 0..len selection, defaulting to the whole stack.
func parseLayerCount(w http.ResponseWriter, raw, name string, layers int) (int, bool) {
	if raw == "" {
		return layers, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > layers {
		badRequest(w, "%s must be a layer count between 0 and %d", name, layers)
		return 0, false
	}
	return n, true
}

// cleanTreePath validates a directory path from the query string.
//
// The path never touches the filesystem — it addresses a node in an in-memory
// tree — but it is still required to be rooted and already clean, so that one
// directory has exactly one addressable form and a cursor issued for "/usr/lib"
// cannot be replayed against "/usr/lib/." as if it were a different query.
func cleanTreePath(w http.ResponseWriter, raw string) (string, bool) {
	if !strings.HasPrefix(raw, "/") {
		badRequest(w, "path must start with /")
		return "", false
	}
	if strings.ContainsRune(raw, 0) {
		badRequest(w, "path must not contain NUL")
		return "", false
	}
	cleaned := path.Clean(raw)
	if cleaned != raw {
		badRequest(w, "path must be already clean (no ., .. or duplicate or trailing slashes)")
		return "", false
	}
	return cleaned, true
}
