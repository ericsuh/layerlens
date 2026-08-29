package domain

import "time"

// Image sources recorded in ImageRecord.Source.
const (
	SourceFixture  = "fixture"
	SourceRegistry = "registry"
	SourceDocker   = "docker"
)

// ImageRecord is a fully analyzed image and the cache's unit of retention
// (ARCHITECTURE §3, §5).
type ImageRecord struct {
	// ID is the config blob digest, i.e. the image ID. It is the canonical
	// identity everywhere: API paths, cache keys and URLs.
	ID Digest `json:"id"`
	// ManifestDigest is the platform manifest digest — what `docker pull`
	// prints. Display only.
	ManifestDigest Digest `json:"manifestDigest,omitempty"`
	// RefNames are display references such as "example:v1".
	RefNames []string `json:"refNames,omitempty"`
	// Source is one of SourceFixture, SourceRegistry, SourceDocker.
	Source string `json:"source"`
	// Platform is always "linux/amd64" for this build.
	Platform string `json:"platform"`
	// CreatedAt mirrors the image config's .created field.
	CreatedAt time.Time `json:"createdAt"`
	// IngestedAt is when layerlens finished analyzing the image.
	IngestedAt time.Time `json:"ingestedAt"`
	// LastUsedAt is the LRU clock (RESEARCH Q7).
	LastUsedAt time.Time `json:"lastUsedAt"`
	// Pinned images (the vendored fixtures) are never evicted.
	Pinned bool `json:"pinned"`
	// Layers are the non-empty rootfs layers in order.
	Layers []Layer `json:"layers"`
	// TotalBytes is the sum of the layers' uncompressed content bytes.
	TotalBytes int64 `json:"totalBytes"`
}

// Layer is one entry of the config's rootfs.diff_ids together with the derived
// identities and the Dockerfile instruction that produced it.
type Layer struct {
	// Index is the 0-based position in rootfs.diff_ids.
	Index int `json:"index"`
	// DiffID is the digest of the uncompressed layer tar. All trunk and
	// ChainID logic keys on this (DECISIONS A4).
	DiffID Digest `json:"diffId"`
	// ChainID is ChainID(L0..LIndex), precomputed at ingest.
	ChainID Digest `json:"chainId"`
	// CompressedDigest is manifest layers[i].digest; empty for a
	// daemon-save without a manifest. Display-only footnote.
	CompressedDigest Digest `json:"compressedDigest,omitempty"`
	// CompressedSize is manifest layers[i].size; 0 when unknown.
	CompressedSize int64 `json:"compressedSize,omitempty"`
	// ContentBytes is the sum of regular-file sizes in this layer's
	// changeset.
	ContentBytes int64 `json:"contentBytes"`
	// EntryCount is the number of changeset entries, whiteouts included.
	EntryCount int `json:"entryCount"`
	// ChangesetDigest is the normalized changeset digest (§3.1). A match is
	// "could have been the same layer", NEVER a cache hit — the API must
	// keep it in a field distinct from owner:"shared".
	ChangesetDigest Digest `json:"changesetDigest"`
	// Instruction is the cleaned created_by; empty when unknown.
	Instruction string `json:"instruction"`
	// InstructionRaw is the verbatim created_by, for the tooltip.
	InstructionRaw string `json:"instructionRaw"`
	// InstructionKnown is false when history and diff_ids counts disagree,
	// in which case no layer gets a guessed instruction (§4.0).
	InstructionKnown bool `json:"instructionKnown"`
}

// HistoryEntry mirrors one OCI image-config history record. It exists so that
// analyze.MapHistory stays free of any registry library's types.
type HistoryEntry struct {
	CreatedBy string `json:"createdBy"`
	Comment   string `json:"comment,omitempty"`
	// EmptyLayer is true when this history item consumes no diff_id
	// (ENV, LABEL, CMD, WORKDIR, EXPOSE, ...).
	EmptyLayer bool `json:"emptyLayer,omitempty"`
}
