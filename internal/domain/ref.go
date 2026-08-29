package domain

// ImageRef is a parsed, allowlist-validated user reference. It is produced by
// internal/imgref; go-containerregistry types never escape that package
// (ARCHITECTURE §2).
type ImageRef struct {
	// Raw is the reference exactly as the user typed it.
	Raw string `json:"raw"`
	// Registry is the resolved registry host, e.g. "index.docker.io" or
	// "ghcr.io". Docker Hub shorthand is expanded.
	Registry string `json:"registry"`
	// Repository is the full repository path, e.g. "library/alpine".
	Repository string `json:"repository"`
	// Tag is empty for digest-only references.
	Tag string `json:"tag,omitempty"`
	// Digest is empty for tag-only references.
	Digest Digest `json:"digest,omitempty"`
}
