package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ericsuh/layerlens/internal/domain"
	"github.com/ericsuh/layerlens/internal/server"
)

func TestImagesEndpoint(t *testing.T) {
	var got server.ImageList
	getJSON(t, apiServer(t), server.APIPrefix+"/images", &got)

	require.Len(t, got.Images, 10)
	refs := make([]string, 0, len(got.Images))
	for _, img := range got.Images {
		assert.Equal(t, domain.SourceFixture, img.Source)
		assert.True(t, img.Pinned, "%v: vendored fixtures are pinned", img.RefNames)
		assert.Equal(t, "linux/amd64", img.Platform)
		assert.Positive(t, img.LayerCount)
		assert.Positive(t, img.TotalBytes)
		assert.False(t, img.CreatedAt.IsZero())
		assert.False(t, img.IngestedAt.IsZero())
		refs = append(refs, img.RefNames...)
	}
	assert.Equal(t, []string{
		"disjoint:a", "disjoint:b",
		"edgecase:opaque", "edgecase:plain",
		"example:v1", "example:v2",
		"prefix:base", "prefix:extended",
		"wide:v1", "wide:v2",
	}, refs, "the listing order is stable so the picker does not reshuffle")
}

// TestImagesEndpointEmitsEmptyArray: a null would force every client to
// null-check a list it can only iterate.
func TestImagesEndpointEmitsEmptyArray(t *testing.T) {
	srv := server.New(server.Options{Logger: discardLogger(), UI: http.NotFoundHandler()})
	resp := doOn(t, srv, http.MethodGet, server.APIPrefix+"/images")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"images":[]}`, body(t, resp))
}

func TestImageDetail(t *testing.T) {
	srv := apiServer(t)
	imageID := id(t, "example:v2")

	var got server.ImageDetail
	getJSON(t, srv, server.APIPrefix+"/images/"+string(imageID), &got)

	assert.Equal(t, imageID, got.ID)
	assert.Equal(t, []string{"example:v2"}, got.RefNames)
	assert.NotEmpty(t, got.ManifestDigest)
	require.Len(t, got.Layers, 8)
	for i, layer := range got.Layers {
		assert.Equal(t, i, layer.Index)
		assert.NoError(t, layer.DiffID.Validate())
		assert.NoError(t, layer.ChainID.Validate())
		assert.True(t, layer.InstructionKnown, "the fixture's history maps cleanly onto its layers")
		assert.NotEmpty(t, layer.InstructionRaw)
	}
	assert.Equal(t, "WORKDIR /app", got.Layers[4].Instruction)
	assert.Equal(t, "RUN npm install", got.Layers[6].Instruction)
}

func TestImageNotFound(t *testing.T) {
	srv := apiServer(t)
	absent := domain.Digest("sha256:" + strings.Repeat("0", 64))

	got := getError(t, srv, server.APIPrefix+"/images/"+string(absent), http.StatusNotFound)
	assert.Equal(t, server.CodeImageNotFound, got.Error.Code)
	assert.Equal(t, string(absent), got.Error.Details["id"])
}

// TestImageIDValidation: the id becomes a cache path component downstream, so a
// malformed one is rejected at the edge rather than deeper in (§7.3).
func TestImageIDValidation(t *testing.T) {
	srv := apiServer(t)
	for _, raw := range []string{
		"not-a-digest",
		"sha256:short",
		"sha256:" + strings.Repeat("A", 64),
		"sha512:" + strings.Repeat("a", 64),
	} {
		t.Run(raw, func(t *testing.T) {
			got := getError(t, srv, server.APIPrefix+"/images/"+raw, http.StatusBadRequest)
			assert.Equal(t, server.CodeBadRequest, got.Error.Code)
		})
	}

	// A traversal-shaped id never even reaches the handler: an escaped path
	// decodes to a non-canonical one, which is not a route inside /api at
	// all. Defense in depth — the digest validation above would reject it
	// too.
	got := getError(t, srv, server.APIPrefix+"/images/..%2F..%2Fetc%2Fpasswd", http.StatusNotFound)
	assert.Equal(t, server.CodeNotFound, got.Error.Code)
}

func TestMetaEndpoint(t *testing.T) {
	cache := seeded(t)

	var got server.MetaResponse
	getJSON(t, apiServer(t), server.APIPrefix+"/meta", &got)

	assert.Equal(t, "test", got.Version)
	assert.Equal(t, cache.store.UsedBytes(), got.CacheBytesUsed)
	assert.Equal(t, cache.store.MaxBytes(), got.CacheMaxBytes)
	assert.Positive(t, got.CacheBytesUsed)
	assert.NotNil(t, got.AllowedRegistries)
}

// TestHealthzGatedUntilFixturesLoaded: a supervisor waiting on /healthz must
// wait for a server that can actually answer, not merely one that is listening.
func TestHealthzGatedUntilFixturesLoaded(t *testing.T) {
	loaded := false
	srv := apiServer(t, func(o *server.Options) {
		o.Ready = func() bool { return loaded }
	})

	resp := doOn(t, srv, http.MethodGet, "/healthz")
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "loading", body(t, resp))

	loaded = true
	resp = doOn(t, srv, http.MethodGet, "/healthz")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", body(t, resp))
}

// TestAPIMethodNotAllowed: a wrong method on a real route must still produce
// the JSON envelope and an Allow header, never ServeMux's plain-text 405.
func TestAPIMethodNotAllowed(t *testing.T) {
	srv := apiServer(t)
	for _, path := range []string{
		"/images",
		"/images/sha256:" + strings.Repeat("a", 64),
		"/diff/layers",
		"/diff/tree",
		"/meta",
	} {
		t.Run(path, func(t *testing.T) {
			resp := doOn(t, srv, http.MethodPost, server.APIPrefix+path)
			raw := body(t, resp)

			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			assert.Equal(t, http.MethodGet, resp.Header.Get("Allow"))
			assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
			var got server.APIError
			require.NoError(t, json.Unmarshal([]byte(raw), &got))
			assert.Equal(t, server.CodeMethodNotAllowed, got.Error.Code)
		})
	}
}
