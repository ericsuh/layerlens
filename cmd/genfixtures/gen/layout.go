package gen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ericsuh/layerlens/internal/domain"
)

// ociLayoutVersion is the only image-layout version the spec defines.
const ociLayoutVersion = "1.0.0"

// descriptor is an OCI content descriptor.
type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      domain.Digest     `json:"digest"`
	Size        int64             `json:"size"`
	Platform    *platform         `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type manifestDoc struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type indexDoc struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

type layoutDoc struct {
	ImageLayoutVersion string `json:"imageLayoutVersion"`
}

type configDoc struct {
	Created      time.Time    `json:"created"`
	Architecture string       `json:"architecture"`
	OS           string       `json:"os"`
	Config       runConfigDoc `json:"config"`
	RootFS       rootFSDoc    `json:"rootfs"`
	History      []historyDoc `json:"history"`
}

type runConfigDoc struct {
	Env        []string          `json:"Env,omitempty"`
	Entrypoint []string          `json:"Entrypoint,omitempty"`
	Cmd        []string          `json:"Cmd,omitempty"`
	WorkingDir string            `json:"WorkingDir,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
}

type rootFSDoc struct {
	Type    string          `json:"type"`
	DiffIDs []domain.Digest `json:"diff_ids"`
}

type historyDoc struct {
	Created    time.Time `json:"created"`
	CreatedBy  string    `json:"created_by,omitempty"`
	Comment    string    `json:"comment,omitempty"`
	EmptyLayer bool      `json:"empty_layer,omitempty"`
}

// PairReport summarizes what WritePair produced, for the CLI and for tests.
type PairReport struct {
	Name string
	Doc  string
	// Images maps in spec order to per-image summaries.
	Images []ImageReport
	// Bytes is the total on-disk size of the layout directory.
	Bytes int64
	// BlobCount is the number of distinct blobs written (config, manifest
	// and layer blobs together).
	BlobCount int
}

// ImageReport summarizes one generated image.
type ImageReport struct {
	Ref            string
	ID             domain.Digest
	ManifestDigest domain.Digest
	DiffIDs        []domain.Digest
	// ContentBytes is the sum of the uncompressed layer tars.
	ContentBytes int64
	// BlobBytes is the sum of the compressed layer blobs.
	BlobBytes int64
}

// WritePair renders one PairSpec into a self-contained OCI image layout under
// root/<pair.Name>.
//
// The directory is wiped first: regeneration must not leave orphaned blobs
// behind, or "regenerate and see no diff" would silently stop being a check on
// anything. Blobs are content-addressed, so the two images of a pair
// automatically share the storage for every layer they have in common — which
// is exactly the property the golden pair is about.
func WritePair(root string, pair *PairSpec) (*PairReport, error) {
	dir := filepath.Join(root, pair.Name)
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755); err != nil {
		return nil, err
	}

	blobs := newBlobStore(dir)
	report := &PairReport{Name: pair.Name, Doc: pair.Doc}
	idx := indexDoc{SchemaVersion: 2, MediaType: MediaTypeIndex}

	for i := range pair.Images {
		img := &pair.Images[i]
		imgReport, mfstDesc, err := writeImage(blobs, img)
		if err != nil {
			return nil, fmt.Errorf("gen: image %s: %w", img.Ref, err)
		}
		idx.Manifests = append(idx.Manifests, mfstDesc)
		report.Images = append(report.Images, *imgReport)
	}

	if err := writeJSONFile(filepath.Join(dir, "oci-layout"), layoutDoc{ImageLayoutVersion: ociLayoutVersion}); err != nil {
		return nil, err
	}
	if err := writeJSONFile(filepath.Join(dir, "index.json"), idx); err != nil {
		return nil, err
	}
	if err := blobs.flush(); err != nil {
		return nil, err
	}

	report.BlobCount = blobs.count()
	size, err := dirSize(dir)
	if err != nil {
		return nil, err
	}
	report.Bytes = size
	return report, nil
}

// writeImage builds every layer of one image, stores its blobs, and returns
// the index descriptor that advertises it.
func writeImage(blobs *blobStore, img *ImageSpec) (*ImageReport, descriptor, error) {
	cfg := configDoc{
		Created:      img.Created.UTC(),
		Architecture: Architecture,
		OS:           OS,
		Config: runConfigDoc{
			Env:        img.Env,
			Entrypoint: img.Entrypoint,
			Cmd:        img.Cmd,
			WorkingDir: img.WorkingDir,
			Labels:     img.Labels,
		},
		RootFS: rootFSDoc{Type: "layers"},
	}
	mfst := manifestDoc{SchemaVersion: 2, MediaType: MediaTypeManifest}
	report := &ImageReport{Ref: img.Ref}

	for i := range img.Layers {
		l := &img.Layers[i]
		hist := historyDoc{
			Created:    img.Created.UTC(),
			CreatedBy:  l.CreatedBy,
			EmptyLayer: l.Empty,
		}
		cfg.History = append(cfg.History, hist)
		if l.Empty {
			// An empty_layer history entry contributes no diff_id.
			// These are what analyze.MapHistory has to skip to keep
			// instructions aligned with layers (§4.0).
			continue
		}
		res, err := buildLayer(l)
		if err != nil {
			return nil, descriptor{}, err
		}
		blobs.put(res.Digest, res.Blob)
		cfg.RootFS.DiffIDs = append(cfg.RootFS.DiffIDs, res.DiffID)
		mfst.Layers = append(mfst.Layers, descriptor{
			MediaType: MediaTypeLayer,
			Digest:    res.Digest,
			Size:      int64(len(res.Blob)),
		})
		report.DiffIDs = append(report.DiffIDs, res.DiffID)
		report.ContentBytes += res.Uncompressed
		report.BlobBytes += int64(len(res.Blob))
	}

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, descriptor{}, err
	}
	configDigest := digestOf(configJSON)
	blobs.put(configDigest, configJSON)
	mfst.Config = descriptor{
		MediaType: MediaTypeConfig,
		Digest:    configDigest,
		Size:      int64(len(configJSON)),
	}

	manifestJSON, err := json.Marshal(mfst)
	if err != nil {
		return nil, descriptor{}, err
	}
	manifestDigest := digestOf(manifestJSON)
	blobs.put(manifestDigest, manifestJSON)

	report.ID = configDigest
	report.ManifestDigest = manifestDigest
	return report, descriptor{
		MediaType:   MediaTypeManifest,
		Digest:      manifestDigest,
		Size:        int64(len(manifestJSON)),
		Platform:    &platform{Architecture: Architecture, OS: OS},
		Annotations: map[string]string{RefNameAnnotation: img.Ref},
	}, nil
}

// blobStore collects blobs by digest and writes them once, in digest order, so
// that neither the filesystem's directory order nor the order in which images
// were built can affect the result.
type blobStore struct {
	dir   string
	byDig map[domain.Digest][]byte
}

func newBlobStore(dir string) *blobStore {
	return &blobStore{dir: dir, byDig: map[domain.Digest][]byte{}}
}

func (s *blobStore) put(d domain.Digest, b []byte) {
	// Content addressing makes a repeat put a no-op by definition: two
	// images sharing a layer share the blob.
	if _, ok := s.byDig[d]; !ok {
		s.byDig[d] = b
	}
}

func (s *blobStore) count() int { return len(s.byDig) }

func (s *blobStore) flush() error {
	digests := make([]domain.Digest, 0, len(s.byDig))
	for d := range s.byDig {
		digests = append(digests, d)
	}
	sort.Slice(digests, func(i, j int) bool { return digests[i] < digests[j] })
	for _, d := range digests {
		hex := d.Hex()
		if hex == "" {
			return fmt.Errorf("gen: malformed blob digest %q", d)
		}
		if err := os.WriteFile(filepath.Join(s.dir, "blobs", "sha256", hex), s.byDig[d], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func digestOf(b []byte) domain.Digest {
	sum := sha256.Sum256(b)
	return domain.DigestFromBytes(sum[:])
}

// writeJSONFile writes an indented JSON document with a trailing newline.
// oci-layout and index.json are not content-addressed, so they are formatted
// for review: a human should be able to read the vendored fixture's tag list
// out of a git diff.
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}
