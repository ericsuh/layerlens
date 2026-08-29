package ingest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"

	"github.com/ericsuh/layerlens/internal/domain"
)

// RefNameAnnotation is how an OCI layout advertises the display reference of a
// manifest. It is what oras, skopeo and crane all write for a tagged manifest,
// and it is the convention the vendored fixtures follow (ARCHITECTURE §9.2).
const RefNameAnnotation = "org.opencontainers.image.ref.name"

// layoutMarkerFile is the file whose presence makes a directory an OCI image
// layout. Directory discovery keys on it rather than on naming.
const layoutMarkerFile = "oci-layout"

// LayoutImage is one analyzable image found in an OCI layout directory.
type LayoutImage struct {
	// Ref is the display reference from the ref-name annotation. It may be
	// empty for an untagged manifest.
	Ref string
	// ManifestDigest identifies the manifest inside the layout.
	ManifestDigest domain.Digest
	// Image is the go-containerregistry handle; it never leaves this
	// package's callers except as an argument back into Ingest.
	Image v1.Image
}

// OpenLayout lists the linux/amd64 images of an OCI image layout directory.
//
// Two classes of manifest are deliberately skipped rather than reported as
// errors, because both appear in perfectly normal images built by BuildKit:
// manifests for another platform, and attestation manifests, which advertise
// themselves as `unknown/unknown` and would otherwise show up in the picker as
// phantom images (DECISIONS risk 4).
func OpenLayout(dir string) ([]LayoutImage, error) {
	path, err := layout.FromPath(dir)
	if err != nil {
		return nil, fmt.Errorf("ingest: open OCI layout %s: %w", dir, err)
	}
	idx, err := path.ImageIndex()
	if err != nil {
		return nil, fmt.Errorf("ingest: read index of %s: %w", dir, err)
	}
	manifest, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("ingest: read index manifest of %s: %w", dir, err)
	}

	out := make([]LayoutImage, 0, len(manifest.Manifests))
	for _, desc := range manifest.Manifests {
		if !wantedPlatform(desc.Platform) {
			continue
		}
		if desc.MediaType.IsIndex() {
			// A nested index inside a layout is legal but is not
			// something the fixtures produce; recursing would need
			// its own platform resolution and has no caller.
			continue
		}
		img, err := idx.Image(desc.Digest)
		if err != nil {
			return nil, fmt.Errorf("ingest: load image %s from %s: %w", desc.Digest, dir, err)
		}
		mfstDigest, err := domain.ParseDigest(desc.Digest.String())
		if err != nil {
			return nil, fmt.Errorf("ingest: manifest digest in %s: %w", dir, err)
		}
		out = append(out, LayoutImage{
			Ref:            desc.Annotations[RefNameAnnotation],
			ManifestDigest: mfstDigest,
			Image:          img,
		})
	}
	// Stable order so a startup load logs the same sequence every time.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Ref != out[j].Ref {
			return out[i].Ref < out[j].Ref
		}
		return out[i].ManifestDigest < out[j].ManifestDigest
	})
	return out, nil
}

// wantedPlatform reports whether a manifest descriptor is the linux/amd64
// image we analyze. A descriptor with no platform at all is accepted: a
// single-image layout is not required to declare one.
func wantedPlatform(p *v1.Platform) bool {
	if p == nil {
		return true
	}
	return p.OS == PlatformOS && p.Architecture == PlatformArch
}

// DiscoverLayouts returns the OCI layout directories under root, in a stable
// order. root itself counts if it is a layout.
//
// Only one level of subdirectory is scanned: the fixtures convention is one
// layout directory per image pair directly under fixtures/ (ARCHITECTURE
// §9.2), and walking arbitrarily deep would turn a mistyped --fixtures-dir
// into a filesystem crawl.
func DiscoverLayouts(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: fixtures directory %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ingest: fixtures path %s is not a directory", root)
	}
	if isLayoutDir(root) {
		return []string{root}, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: read fixtures directory %s: %w", root, err)
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(root, e.Name())
		if isLayoutDir(candidate) {
			dirs = append(dirs, candidate)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func isLayoutDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, layoutMarkerFile))
	return err == nil && !info.IsDir()
}

// ErrNoLayouts reports a fixtures directory that holds no OCI layouts.
var ErrNoLayouts = errors.New("ingest: no OCI image layouts found")
