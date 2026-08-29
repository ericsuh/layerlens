package analyze

import "github.com/ericsuh/layerlens/internal/domain"

// ChangesetDigestWithVersion exposes the version-parameterized digest to the
// package's external tests.
func ChangesetDigestWithVersion(version byte, entries []domain.Entry) domain.Digest {
	return changesetDigest(version, entries)
}
