package ingest_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/ingest"
)

// fakeDaemon is a Docker daemon that exists only in memory.
type fakeDaemon struct {
	pingErr    error
	listErr    error
	saveErr    error
	images     []ingest.DockerInspect
	saves      map[string][]byte
	saveCalls  int
	inspectErr error
}

func (f *fakeDaemon) Ping(context.Context) error { return f.pingErr }

func (f *fakeDaemon) List(context.Context) ([]ingest.DockerInspect, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.images, nil
}

func (f *fakeDaemon) Inspect(_ context.Context, ref string) (*ingest.DockerInspect, error) {
	if f.inspectErr != nil {
		return nil, f.inspectErr
	}
	for i := range f.images {
		if f.images[i].ID == ref {
			return &f.images[i], nil
		}
		for _, tag := range f.images[i].RepoTags {
			if tag == ref {
				return &f.images[i], nil
			}
		}
	}
	return nil, fmt.Errorf("no such image: %s", ref)
}

func (f *fakeDaemon) Save(_ context.Context, ref string) (io.ReadCloser, error) {
	f.saveCalls++
	if f.saveErr != nil {
		return nil, f.saveErr
	}
	body, ok := f.saves[ref]
	if !ok {
		return nil, fmt.Errorf("no save for %s", ref)
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *fakeDaemon) Close() error { return nil }

func newFakeDocker(t *testing.T, daemon *fakeDaemon, images domain.ImageStore) *ingest.Docker {
	t.Helper()
	d := ingest.NewDocker(ingest.DockerOptions{
		Host:   "unix:///fake/docker.sock",
		Images: images,
		Logger: discard(),
		Dial: func(string) (ingest.DockerAPI, error) {
			return daemon, nil
		},
	})
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// A server with no Docker must not surface an error anywhere: the tab simply
// reports itself unavailable (§6.3, DESIGN state #4).
func TestDockerListingWithoutASocket(t *testing.T) {
	d := ingest.NewDocker(ingest.DockerOptions{Logger: discard()})
	listing, err := d.List(context.Background())
	require.NoError(t, err)
	assert.False(t, listing.Available)
	assert.Contains(t, listing.Reason, "No Docker socket found")
	assert.Empty(t, listing.Images)
	assert.NotNil(t, listing.Images, "the client's empty state keys on length, so null is not acceptable")
}

// `--docker-host off` and "nothing was found" both leave the host empty, but
// they are different answers and the deployed systemd unit sets `off` by
// default — so a deployment must not tell every operator that their server has
// no Docker socket when they are the one who turned the source off.
func TestDockerListingWhenExplicitlyDisabled(t *testing.T) {
	d := ingest.NewDocker(ingest.DockerOptions{Disabled: true, Logger: discard()})
	listing, err := d.List(context.Background())
	require.NoError(t, err)
	assert.False(t, listing.Available)
	assert.Contains(t, listing.Reason, "turned off")
	assert.NotContains(t, listing.Reason, "No Docker socket found")
	assert.NotNil(t, listing.Images)
}

func TestDockerListingWhenThePingFails(t *testing.T) {
	d := newFakeDocker(t, &fakeDaemon{pingErr: errors.New("permission denied")}, nil)
	listing, err := d.List(context.Background())
	require.NoError(t, err)
	assert.False(t, listing.Available)
	assert.Contains(t, listing.Reason, "not reachable")
}

func TestDockerListingCrossReferencesAnalyzedImages(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	ingester := newIngester(store)
	img := fixtureImage(t, "example", "example:v1")
	res, err := ingester.Ingest(context.Background(), img, ingest.Meta{
		Source: domain.SourceFixture, RefNames: []string{"example:v1", "example:v1-by-index"},
	})
	require.NoError(t, err)
	analyzed := res.Record.ID

	// The second row is the containerd-store case: the daemon identifies a
	// multi-platform image by its *index* digest, which is not the config
	// digest layerlens keys on, so only the reference can match it.
	daemon := &fakeDaemon{images: []ingest.DockerInspect{
		{ID: string(analyzed), RepoTags: []string{"example:v1"}, SizeBytes: 4096, OS: "linux", Architecture: "amd64"},
		{ID: "sha256:" + fmt.Sprintf("%064d", 9), RepoTags: []string{"example:v1-by-index"}, SizeBytes: 4096},
		{ID: "sha256:" + fmt.Sprintf("%064d", 7), RepoTags: []string{"other:latest"}, SizeBytes: 99, OS: "linux", Architecture: "amd64"},
		{ID: "sha256:" + fmt.Sprintf("%064d", 8), RepoTags: nil},
	}}
	listing, err := newFakeDocker(t, daemon, store).List(context.Background())
	require.NoError(t, err)

	require.True(t, listing.Available)
	require.Len(t, listing.Images, 3, "an untagged image cannot be named back to POST /pulls, so it is not offered")
	byRef := map[string]domain.DockerImageSummary{}
	for _, row := range listing.Images {
		byRef[row.Reference] = row
	}
	assert.True(t, byRef["example:v1"].AlreadyAnalyzed)
	assert.True(t, byRef["example:v1-by-index"].AlreadyAnalyzed,
		"an image the daemon identifies by its index digest is still matched, by reference")
	assert.Equal(t, analyzed, byRef["example:v1-by-index"].AnalyzedID)
	assert.Equal(t, analyzed, byRef["example:v1"].AnalyzedID)
	assert.Equal(t, "linux/amd64", byRef["example:v1"].Platform)
	assert.False(t, byRef["other:latest"].AlreadyAnalyzed)
	assert.Empty(t, byRef["other:latest"].AnalyzedID)
}

func TestDockerListingPropagatesARealFailure(t *testing.T) {
	d := newFakeDocker(t, &fakeDaemon{listErr: errors.New("timeout")}, nil)
	_, err := d.List(context.Background())
	require.Error(t, err, "a daemon that answered the ping and then failed is a real error (DESIGN state #6)")
}

// --- save-stream parsing ---------------------------------------------------

// The two shapes a `docker save` can have. Engine 29 with the containerd store
// writes the OCI layout (with a legacy manifest.json alongside); a
// graphdriver-backed daemon writes the legacy form alone.
func TestDockerSaveStreamShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(*testing.T, v1.Image) []byte
	}{
		{"oci layout", ociSaveTar},
		{"oci layout with a nested multi-platform index", nestedOCISaveTar},
		{"legacy manifest.json", legacySaveTar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Forces the streaming path: with the production threshold
			// every member of a fixture image is small enough to be
			// buffered, and the interesting code would never run.
			ingest.SetSaveBufferLimit(t, 10_000)

			img := fixtureImage(t, "example", "example:v1")
			store := newStore(t, t.TempDir(), 1<<30)
			reporter := &RecordingReporter{}
			res, err := newIngester(store).IngestDockerSave(context.Background(),
				bytes.NewReader(tc.build(t, img)),
				ingest.Meta{Source: domain.SourceDocker, RefNames: []string{"example:v1"}, Progress: reporter})
			require.NoError(t, err)

			cfg, err := img.ConfigFile()
			require.NoError(t, err)
			want, err := img.ConfigName()
			require.NoError(t, err)
			require.NotNil(t, res.Record)
			assert.Equal(t, want.String(), string(res.Record.ID),
				"the image id is the digest of the config blob exactly as it was serialized")
			assert.Len(t, res.Record.Layers, len(cfg.RootFS.DiffIDs))
			assert.Equal(t, domain.SourceDocker, res.Record.Source)
			for n, diffID := range cfg.RootFS.DiffIDs {
				assert.Equal(t, diffID.String(), string(res.Record.Layers[n].DiffID))
				assert.NotEmpty(t, res.Record.Layers[n].ChangesetDigest)
				assert.NotEmpty(t, res.Record.Layers[n].ChainID)
			}
			assert.Positive(t, reporter.Bytes().Load(), "the pass reports real bytes, not a simulation")
		})
	}
}

// A save whose metadata precedes its blobs lets the parser skip a layer it
// already holds by draining the bytes instead of re-hashing them
// (DECISIONS A2).
func TestDockerSaveSkipsLayersAlreadyIndexed(t *testing.T) {
	ingest.SetSaveBufferLimit(t, 10_000)
	img := fixtureImage(t, "example", "example:v1")
	store := newStore(t, t.TempDir(), 1<<30)
	ingester := newIngester(store)

	_, err := ingester.Ingest(context.Background(), img, ingest.Meta{Source: domain.SourceFixture})
	require.NoError(t, err)

	// Metadata first: this is the ordering in which the optimization can
	// apply at all.
	reporter := &RecordingReporter{}
	res, err := ingester.IngestDockerSave(context.Background(),
		bytes.NewReader(ociSaveTarMetadataFirst(t, img)),
		ingest.Meta{Source: domain.SourceDocker, Progress: reporter})
	require.NoError(t, err)
	assert.Positive(t, res.LayersSkipped)
	assert.Zero(t, res.LayersIndexed, "nothing had to be hashed a second time")
	_, started, _, skipped := reporter.Snapshot()
	assert.NotEmpty(t, skipped, "at least one layer took the drain-without-hashing path")
	assert.Empty(t, started, "and no layer was opened for indexing")
}

func TestDockerSaveWithoutAnAmd64Image(t *testing.T) {
	ingest.SetSaveBufferLimit(t, 10_000)
	img := fixtureImage(t, "example", "example:v1")
	tarBytes := ociSaveTarWithPlatform(t, img, &v1.Platform{OS: "linux", Architecture: "arm64"})
	store := newStore(t, t.TempDir(), 1<<30)
	_, err := newIngester(store).IngestDockerSave(context.Background(),
		bytes.NewReader(tarBytes), ingest.Meta{Source: domain.SourceDocker})
	require.ErrorIs(t, err, ingest.ErrNoAmd64Manifest)
}

func TestDockerSaveRejectsGarbage(t *testing.T) {
	store := newStore(t, t.TempDir(), 1<<30)
	_, err := newIngester(store).IngestDockerSave(context.Background(),
		bytes.NewReader([]byte("this is not a tar")), ingest.Meta{Source: domain.SourceDocker})
	require.Error(t, err)
}

// --- save tar builders -----------------------------------------------------

type tarMember struct {
	name string
	body []byte
}

func writeTar(t *testing.T, members []tarMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: m.name, Mode: 0o444, Size: int64(len(m.body)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write(m.body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// imageMembers returns the blob members of an image plus its manifest and
// config bytes.
func imageMembers(t *testing.T, img v1.Image) (blobs []tarMember, manifest, config []byte, manifestDigest v1.Hash) {
	t.Helper()
	config, err := img.RawConfigFile()
	require.NoError(t, err)
	manifest, err = img.RawManifest()
	require.NoError(t, err)
	manifestDigest, err = img.Digest()
	require.NoError(t, err)
	configDigest, err := img.ConfigName()
	require.NoError(t, err)

	blobs = []tarMember{
		{name: "blobs/sha256/" + configDigest.Hex, body: config},
		{name: "blobs/sha256/" + manifestDigest.Hex, body: manifest},
	}
	layers, err := img.Layers()
	require.NoError(t, err)
	for _, layer := range layers {
		digest, err := layer.Digest()
		require.NoError(t, err)
		rc, err := layer.Compressed()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		blobs = append(blobs, tarMember{name: "blobs/sha256/" + digest.Hex, body: body})
	}
	// The real Engine writes blobs in digest order, then index.json.
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].name < blobs[j].name })
	return blobs, manifest, config, manifestDigest
}

func indexDoc(t *testing.T, manifestDigest v1.Hash, size int, platform *v1.Platform, annotations map[string]string) []byte {
	t.Helper()
	desc := map[string]any{
		"mediaType":   "application/vnd.oci.image.manifest.v1+json",
		"digest":      manifestDigest.String(),
		"size":        size,
		"annotations": annotations,
	}
	if platform != nil {
		desc["platform"] = map[string]any{"os": platform.OS, "architecture": platform.Architecture}
	}
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests":     []any{desc},
	})
	require.NoError(t, err)
	return raw
}

// ociSaveTar mirrors an Engine 29 save: blobs first, index.json and
// manifest.json last.
func ociSaveTar(t *testing.T, img v1.Image) []byte {
	t.Helper()
	blobs, manifest, _, manifestDigest := imageMembers(t, img)
	members := append([]tarMember{}, blobs...)
	members = append(members,
		tarMember{name: "index.json", body: indexDoc(t, manifestDigest, len(manifest), nil,
			map[string]string{"io.containerd.image.name": "example:v1"})},
		tarMember{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
	)
	return writeTar(t, members)
}

func ociSaveTarMetadataFirst(t *testing.T, img v1.Image) []byte {
	t.Helper()
	blobs, manifest, _, manifestDigest := imageMembers(t, img)
	members := []tarMember{
		{name: "index.json", body: indexDoc(t, manifestDigest, len(manifest), nil, nil)},
		{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
	}
	// The manifest and config blobs come before the layer blobs.
	var layers []tarMember
	for _, blob := range blobs {
		if len(blob.body) < 4096 {
			members = append(members, blob)
		} else {
			layers = append(layers, blob)
		}
	}
	return writeTar(t, append(members, layers...))
}

func ociSaveTarWithPlatform(t *testing.T, img v1.Image, platform *v1.Platform) []byte {
	t.Helper()
	blobs, manifest, _, manifestDigest := imageMembers(t, img)
	members := append([]tarMember{}, blobs...)
	members = append(members,
		tarMember{name: "index.json", body: indexDoc(t, manifestDigest, len(manifest), platform, nil)},
		tarMember{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
	)
	return writeTar(t, members)
}

// nestedOCISaveTar is the shape a multi-platform image really saves as: a
// top-level index pointing at another index, which holds the per-platform
// manifests plus BuildKit's unknown/unknown attestation entry.
func nestedOCISaveTar(t *testing.T, img v1.Image) []byte {
	t.Helper()
	blobs, manifest, _, manifestDigest := imageMembers(t, img)

	inner, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []any{
			map[string]any{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    fmt.Sprintf("sha256:%064x", 1),
				"size":      2,
				"platform":  map[string]any{"os": "unknown", "architecture": "unknown"},
			},
			map[string]any{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    fmt.Sprintf("sha256:%064x", 2),
				"size":      2,
				"platform":  map[string]any{"os": "linux", "architecture": "arm64"},
			},
			map[string]any{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    manifestDigest.String(),
				"size":      len(manifest),
				"platform":  map[string]any{"os": "linux", "architecture": "amd64"},
			},
		},
	})
	require.NoError(t, err)
	innerDigest := sha256Hash(inner)

	outer, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []any{map[string]any{
			"mediaType":   "application/vnd.oci.image.index.v1+json",
			"digest":      innerDigest,
			"size":        len(inner),
			"annotations": map[string]string{"io.containerd.image.name": "example:v1"},
		}},
	})
	require.NoError(t, err)

	members := append([]tarMember{}, blobs...)
	members = append(members,
		tarMember{name: "blobs/sha256/" + innerDigest[len("sha256:"):], body: inner},
		tarMember{name: "index.json", body: outer},
		tarMember{name: "oci-layout", body: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
	)
	return writeTar(t, members)
}

// legacySaveTar is the pre-OCI shape: manifest.json naming a config file and
// per-layer tars, with no blobs/ directory at all.
func legacySaveTar(t *testing.T, img v1.Image) []byte {
	t.Helper()
	config, err := img.RawConfigFile()
	require.NoError(t, err)
	configName := "config.json"

	var members []tarMember
	var layerPaths []string
	layers, err := img.Layers()
	require.NoError(t, err)
	for n, layer := range layers {
		rc, err := layer.Uncompressed()
		require.NoError(t, err)
		body, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		path := fmt.Sprintf("layer%d/layer.tar", n)
		members = append(members, tarMember{name: path, body: body})
		layerPaths = append(layerPaths, path)
	}
	manifest, err := json.Marshal([]map[string]any{{
		"Config":   configName,
		"RepoTags": []string{"example:v1"},
		"Layers":   layerPaths,
	}})
	require.NoError(t, err)
	members = append(members,
		tarMember{name: configName, body: config},
		tarMember{name: "manifest.json", body: manifest},
	)
	return writeTar(t, members)
}
