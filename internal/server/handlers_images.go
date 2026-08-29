package server

import (
	"net/http"

	"github.com/ericsuh/layerlens/internal/domain"
)

// handleImages serves GET /api/v1/images (§6.2).
func (s *Server) handleImages(w http.ResponseWriter, r *http.Request) {
	records, err := s.images.Images(r.Context())
	if err != nil {
		s.writeStoreError(w, r, err, "")
		return
	}
	// A non-nil empty slice, so the JSON is `{"images":[]}` and not
	// `{"images":null}`: the client's empty state keys on length.
	out := ImageList{Images: make([]ImageSummary, 0, len(records))}
	for i := range records {
		out.Images = append(out.Images, summaryOf(&records[i]))
	}
	s.writeJSON(w, out)
}

// handleImage serves GET /api/v1/images/{id} (§6.2).
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	id, ok := parseImageID(w, r.PathValue("id"), "id")
	if !ok {
		return
	}
	rec, err := s.images.Image(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, r, err, id)
		return
	}
	s.touch(r, id)
	s.writeJSON(w, detailOf(rec))
}

// handleMeta serves GET /api/v1/meta (§6.6).
func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	out := MetaResponse{Version: s.version, AllowedRegistries: s.allowed}
	if out.AllowedRegistries == nil {
		out.AllowedRegistries = []string{}
	}
	if s.cache != nil {
		out.CacheBytesUsed = s.cache.UsedBytes()
		out.CacheMaxBytes = s.cache.MaxBytes()
	}
	s.writeJSON(w, out)
}

// parseImageID validates an image id from the path or the query string and
// writes the error envelope itself when it is malformed.
//
// The validation is not cosmetic: the id becomes a cache path component
// downstream, and domain.Digest.Validate is the §7.3 choke point that keeps a
// traversal-shaped digest out of filepath.Join.
func parseImageID(w http.ResponseWriter, raw, param string) (domain.Digest, bool) {
	if raw == "" {
		badRequest(w, "%s is required", param)
		return "", false
	}
	id, err := domain.ParseDigest(raw)
	if err != nil {
		badRequest(w, "%s must be a sha256 digest of the form sha256:<64 hex characters>", param)
		return "", false
	}
	return id, true
}

// touch bumps an image's LRU clock. A failure is logged and swallowed: the
// user asked for a comparison, not for a cache bookkeeping update, and the
// worst consequence of a missed bump is a slightly premature eviction.
func (s *Server) touch(r *http.Request, ids ...domain.Digest) {
	for _, id := range ids {
		if err := s.images.Touch(r.Context(), id); err != nil {
			s.log.Debug("touch image", "id", id, "err", err)
		}
	}
}
