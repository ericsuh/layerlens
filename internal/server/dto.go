package server

import (
	"time"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// The structs in this file are the wire contract of ARCHITECTURE §6.2–§6.6 and
// mirror the TypeScript interfaces the SPA declares. They are deliberately
// separate from the domain types: the domain model carries things the wire
// should not (the raw entry list, the LRU clock), and the wire carries things
// the domain should not (a JSON-friendly status string, derived denominators).
//
// One honesty rule governs the whole file (§3, §4.5): `owner: "shared"` means
// the two images genuinely share a cached layer — equal DiffIDs on the trunk —
// while `couldBeShared` means only that two *different* layers have equivalent
// changesets under Docker's build-cache rule. They are different fields with
// different meanings, and a changeset match is never reported as a cache hit.

// ImageSummary is the picker's view of one analyzed image (§6.2).
type ImageSummary struct {
	ID         domain.Digest `json:"id"`
	RefNames   []string      `json:"refNames"`
	Source     string        `json:"source"`
	Platform   string        `json:"platform"`
	LayerCount int           `json:"layerCount"`
	TotalBytes int64         `json:"totalBytes"`
	CreatedAt  time.Time     `json:"createdAt"`
	IngestedAt time.Time     `json:"ingestedAt"`
	Pinned     bool          `json:"pinned"`
}

// ImageDetail extends ImageSummary with the layer list (§6.2).
type ImageDetail struct {
	ImageSummary
	ManifestDigest domain.Digest `json:"manifestDigest,omitempty"`
	Layers         []LayerInfo   `json:"layers"`
}

// ImageList is the /images response body.
type ImageList struct {
	Images []ImageSummary `json:"images"`
}

// LayerInfo is one layer as the UI draws it (§6.4).
type LayerInfo struct {
	Index            int           `json:"index"`
	DiffID           domain.Digest `json:"diffId"`
	ChainID          domain.Digest `json:"chainId"`
	CompressedDigest domain.Digest `json:"compressedDigest,omitempty"`
	CompressedSize   int64         `json:"compressedSize,omitempty"`
	ContentBytes     int64         `json:"contentBytes"`
	EntryCount       int           `json:"entryCount"`
	Instruction      string        `json:"instruction"`
	InstructionRaw   string        `json:"instructionRaw"`
	InstructionKnown bool          `json:"instructionKnown"`
}

// Layer ownership values. "shared" is reserved for the trunk and must never be
// used for a could-be-shared match.
const (
	OwnerShared = "shared"
	OwnerLeft   = "left"
	OwnerRight  = "right"
)

// GraphLayer is a LayerInfo positioned in the fork diagram.
type GraphLayer struct {
	LayerInfo
	// Owner is "shared" only for trunk layers, i.e. layers the two images
	// genuinely share in a local layer store.
	Owner string `json:"owner"`
}

// CouldBeSharedEdge is one dotted edge (§6.4). The name and the comment are
// the contract: this is not a cache hit, and the payload never calls it
// "shared".
type CouldBeSharedEdge struct {
	LeftIndex  int `json:"leftIndex"`
	RightIndex int `json:"rightIndex"`
	// DiffIDEqual distinguishes a byte-identical layer tar that merely sits
	// at a different position (true) from two tars with the same content
	// but different bytes, in practice mtimes (false). Neither is a cache
	// hit.
	DiffIDEqual bool `json:"diffIdEqual"`
}

// LayerGraph is the /diff/layers response (§6.4).
type LayerGraph struct {
	Left  ImageSummary `json:"left"`
	Right ImageSummary `json:"right"`
	// TrunkLength is k, the number of leading layers with equal DiffIDs.
	TrunkLength int          `json:"trunkLength"`
	Trunk       []GraphLayer `json:"trunk"`
	LeftBranch  []GraphLayer `json:"leftBranch"`
	RightBranch []GraphLayer `json:"rightBranch"`
	// CouldBeShared holds the dotted edges between the two branches.
	CouldBeShared []CouldBeSharedEdge `json:"couldBeShared"`
	// MaxLayerBytes is the size-bar denominator: the largest contentBytes
	// across both images' layers.
	MaxLayerBytes int64 `json:"maxLayerBytes"`
}

// TreeSideMeta is one side's metadata for a tree row (§6.5).
type TreeSideMeta struct {
	Kind      string `json:"kind"`
	Mode      uint32 `json:"mode"`
	SizeBytes int64  `json:"sizeBytes"`
	// Implicit marks a directory no layer header ever named: it exists
	// only because a child needed a parent, and its mode is the 0755
	// squashing invents (§4.2). Present so the client can render "—"
	// instead of a permission string that is ours, not the image's.
	// Absent means false — a real header supplied these values.
	Implicit   bool   `json:"implicit,omitempty"`
	LinkTarget string `json:"linkTarget,omitempty"`
}

// TreeAgg is the subtree aggregate for a row. Deltas are derivable
// (rightBytes - leftBytes) and are deliberately not duplicated on the wire.
//
// The four side totals are always present because every row renders them. The
// seven change breakdowns are omitted when zero, and an absent field means
// zero: in an unchanged subtree — the overwhelming majority of rows in any
// real image — those seven fields are 130 bytes of `":0,"` per row, which is
// a third of the row and the difference between the §6.5 payload bound holding
// and not. See DECISIONS "Implementation deltas", phase 005.
type TreeAgg struct {
	LeftBytes  int64 `json:"leftBytes"`
	RightBytes int64 `json:"rightBytes"`
	LeftFiles  int64 `json:"leftFiles"`
	RightFiles int64 `json:"rightFiles"`

	AddedBytes         int64 `json:"addedBytes,omitempty"`
	RemovedBytes       int64 `json:"removedBytes,omitempty"`
	ModifiedBytesLeft  int64 `json:"modifiedBytesLeft,omitempty"`
	ModifiedBytesRight int64 `json:"modifiedBytesRight,omitempty"`
	AddedFiles         int64 `json:"addedFiles,omitempty"`
	RemovedFiles       int64 `json:"removedFiles,omitempty"`
	ModifiedFiles      int64 `json:"modifiedFiles,omitempty"`
}

// TreeRow is one entry of a directory listing in the unified diff tree.
type TreeRow struct {
	Name   string        `json:"name"`
	Path   string        `json:"path"`
	Status string        `json:"status"`
	Left   *TreeSideMeta `json:"left,omitempty"`
	Right  *TreeSideMeta `json:"right,omitempty"`
	Agg    TreeAgg       `json:"agg"`
	// HasChildren and ChildCount are POST-filter: with filter=changed they
	// describe what the client will actually receive if it expands the row,
	// which is what "N of M shown" and aria-setsize need.
	HasChildren bool `json:"hasChildren"`
	ChildCount  int  `json:"childCount"`
	// Children is present only for depth=2 requests.
	Children []TreeRow `json:"children,omitempty"`
	// ChildrenTruncated says this row has children the response did not
	// embed — cut at the per-row embedded cap, or by the response-wide
	// embedded budget, in which case `children` is absent entirely. Page
	// them with a request rooted at this row's path.
	ChildrenTruncated bool `json:"childrenTruncated,omitempty"`
}

// TreePage is the /diff/tree response (§6.5).
type TreePage struct {
	Path string    `json:"path"`
	Rows []TreeRow `json:"rows"`
	// NextCursor is absent on the last page for this path+filter.
	NextCursor string `json:"nextCursor,omitempty"`
	// TotalRows is the post-filter number of direct children of Path.
	TotalRows int `json:"totalRows"`
	// MaxSiblingBytes is max(leftBytes+rightBytes) over ALL post-filter
	// children of Path, not just this page, so the relative-size bars do
	// not rescale as the user pages.
	MaxSiblingBytes int64 `json:"maxSiblingBytes"`
	// PathStatus and PathAgg describe Path itself, for the breadcrumb line.
	PathStatus string  `json:"pathStatus"`
	PathAgg    TreeAgg `json:"pathAgg"`
}

// MetaResponse is the /meta body (§6.6).
type MetaResponse struct {
	Version           string   `json:"version"`
	CacheBytesUsed    int64    `json:"cacheBytesUsed"`
	CacheMaxBytes     int64    `json:"cacheMaxBytes"`
	AllowedRegistries []string `json:"allowedRegistries"`
}

// ---------------------------------------------------------------- mapping

func summaryOf(rec *domain.ImageRecord) ImageSummary {
	refs := rec.RefNames
	if refs == nil {
		// A JSON null here would force every client to null-check a
		// list it can only ever iterate.
		refs = []string{}
	}
	return ImageSummary{
		ID:         rec.ID,
		RefNames:   refs,
		Source:     rec.Source,
		Platform:   rec.Platform,
		LayerCount: len(rec.Layers),
		TotalBytes: rec.TotalBytes,
		CreatedAt:  rec.CreatedAt,
		IngestedAt: rec.IngestedAt,
		Pinned:     rec.Pinned,
	}
}

func layerInfoOf(l *domain.Layer) LayerInfo {
	return LayerInfo{
		Index:            l.Index,
		DiffID:           l.DiffID,
		ChainID:          l.ChainID,
		CompressedDigest: l.CompressedDigest,
		CompressedSize:   l.CompressedSize,
		ContentBytes:     l.ContentBytes,
		EntryCount:       l.EntryCount,
		Instruction:      l.Instruction,
		InstructionRaw:   l.InstructionRaw,
		InstructionKnown: l.InstructionKnown,
	}
}

func detailOf(rec *domain.ImageRecord) ImageDetail {
	layers := make([]LayerInfo, 0, len(rec.Layers))
	for i := range rec.Layers {
		layers = append(layers, layerInfoOf(&rec.Layers[i]))
	}
	return ImageDetail{
		ImageSummary:   summaryOf(rec),
		ManifestDigest: rec.ManifestDigest,
		Layers:         layers,
	}
}

func graphLayersOf(layers []domain.Layer, owner string) []GraphLayer {
	out := make([]GraphLayer, 0, len(layers))
	for i := range layers {
		out = append(out, GraphLayer{LayerInfo: layerInfoOf(&layers[i]), Owner: owner})
	}
	return out
}

func edgesOf(edges []analyze.CouldBeSharedEdge) []CouldBeSharedEdge {
	out := make([]CouldBeSharedEdge, 0, len(edges))
	for _, e := range edges {
		out = append(out, CouldBeSharedEdge{
			LeftIndex:   e.LeftIndex,
			RightIndex:  e.RightIndex,
			DiffIDEqual: e.DiffIDEqual,
		})
	}
	return out
}

func aggOf(a *domain.Agg) TreeAgg {
	return TreeAgg{
		LeftBytes:          a.LeftBytes,
		RightBytes:         a.RightBytes,
		LeftFiles:          a.LeftFiles,
		RightFiles:         a.RightFiles,
		AddedBytes:         a.AddedBytes,
		RemovedBytes:       a.RemovedBytes,
		ModifiedBytesLeft:  a.ModifiedBytesLeft,
		ModifiedBytesRight: a.ModifiedBytesRight,
		AddedFiles:         a.AddedFiles,
		RemovedFiles:       a.RemovedFiles,
		ModifiedFiles:      a.ModifiedFiles,
	}
}

func sideMetaOf(m *domain.SideMeta) *TreeSideMeta {
	if m == nil {
		return nil
	}
	return &TreeSideMeta{
		Kind:       m.Kind.String(),
		Mode:       m.Mode,
		SizeBytes:  m.Size,
		Implicit:   m.Implicit,
		LinkTarget: m.LinkTarget,
	}
}
