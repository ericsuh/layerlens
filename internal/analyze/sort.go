package analyze

import (
	"sort"

	"github.com/ericsuh/layerlens/internal/domain"
)

// IsDirRow reports whether a diff row should be presented as a directory.
//
// A path can be a directory on one side and a file on the other. The right
// ("after") side wins when it exists, because that is the state the user is
// looking at; a row that exists only on the left is classified by the left.
func IsDirRow(n *domain.DiffNode) bool {
	if n.Right != nil {
		return n.Right.Kind == domain.KindDir
	}
	if n.Left != nil {
		return n.Left.Kind == domain.KindDir
	}
	return false
}

// SortDiffChildren orders a directory's rows the way the API and the UI show
// them: directories first, then everything else, each group name-ascending
// (ARCHITECTURE §6.5). The order is total — names are unique among siblings —
// so paging over it is stable across requests.
func SortDiffChildren(rows []*domain.DiffNode) {
	sort.Slice(rows, func(i, j int) bool {
		di, dj := IsDirRow(rows[i]), IsDirRow(rows[j])
		if di != dj {
			return di
		}
		return rows[i].Name < rows[j].Name
	})
}
