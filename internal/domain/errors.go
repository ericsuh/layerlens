package domain

import "errors"

var (
	// ErrNotIndexed is returned by a LayerIndexSource when the requested
	// DiffID has never been indexed.
	ErrNotIndexed = errors.New("layer not indexed")

	// ErrNotFound is returned by an ImageStore when the requested image is
	// not present (or has been evicted).
	ErrNotFound = errors.New("not found")
)
