package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

// ErrBadCursor reports a cursor that is malformed, or valid but issued for a
// different query.
var ErrBadCursor = errors.New("invalid cursor")

// Section names in a cursor. Rows are ordered directories-first, then
// everything else, each group name-ascending (analyze.SortDiffChildren), so
// (section, name) is a total order over a directory's rows and therefore a
// sound resume point.
const (
	sectionDir  = "dir"
	sectionFile = "file"
)

// cursorPayload is the decoded form of the opaque cursor. It is JSON rather
// than a packed encoding because it is tiny and because a decodable cursor is
// worth far more when debugging than an unreadable one — it is opaque to the
// client by contract, not by obfuscation.
type cursorPayload struct {
	Section  string `json:"s"`
	LastName string `json:"n"`
	// Query binds the cursor to the request that produced it.
	Query string `json:"q"`
}

// cursorQuery fingerprints the tuple a cursor is valid for (§6.5): the same
// pair, the same layer selection, the same directory and the same filter.
//
// Paging is only coherent within one deterministic row ordering, and each of
// these inputs can change it. A cursor carried across any of them would
// silently skip or repeat rows, so it is rejected instead.
func cursorQuery(key comparisonKey, path, filter string) string {
	h := sha256.New()
	for _, part := range []string{
		string(key.left), fmt.Sprint(key.leftLayers),
		string(key.right), fmt.Sprint(key.rightLayers),
		path, filter,
	} {
		// Length-prefixed so that no combination of values can collide
		// with another by shifting a delimiter.
		fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// encodeCursor renders the resume point after row.
func encodeCursor(query string, row *domain.DiffNode) string {
	payload := cursorPayload{Section: sectionOf(row), LastName: row.Name, Query: query}
	raw, err := json.Marshal(payload)
	if err != nil {
		// cursorPayload is three strings; marshalling cannot fail.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor parses a client-supplied cursor and checks that it belongs to
// this query.
func decodeCursor(raw, query string) (*cursorPayload, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // "no cursor" is a valid, common state
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: not base64", ErrBadCursor)
	}
	var payload cursorPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, fmt.Errorf("%w: not a cursor", ErrBadCursor)
	}
	if payload.Section != sectionDir && payload.Section != sectionFile {
		return nil, fmt.Errorf("%w: unknown section %q", ErrBadCursor, payload.Section)
	}
	if payload.Query != query {
		// Deliberately an error rather than a silent restart: results
		// are deterministic, so the client refetching from page 1 is
		// loss-free, and hiding the mismatch would hide a real bug.
		return nil, fmt.Errorf("%w: issued for a different query", ErrBadCursor)
	}
	return &payload, nil
}

func sectionOf(n *domain.DiffNode) string {
	if analyze.IsDirRow(n) {
		return sectionDir
	}
	return sectionFile
}

// sectionRank maps a section onto its sort position: directories first.
func sectionRank(section string) int {
	if section == sectionDir {
		return 0
	}
	return 1
}

// pageStart returns the index of the first row after the cursor.
//
// rows are already in the canonical order, so this is a binary search for the
// first row that sorts strictly after (section, lastName). A row that has
// since disappeared does not break the walk: the search lands on whatever now
// occupies that position, which is exactly the next unseen row.
func pageStart(rows []*domain.DiffNode, cur *cursorPayload) int {
	if cur == nil {
		return 0
	}
	rank := sectionRank(cur.Section)
	return sort.Search(len(rows), func(i int) bool {
		r := sectionRank(sectionOf(rows[i]))
		if r != rank {
			return r > rank
		}
		return rows[i].Name > cur.LastName
	})
}
