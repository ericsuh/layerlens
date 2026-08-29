package analyze_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ericsuh/layerlens/internal/analyze"
	"github.com/ericsuh/layerlens/internal/domain"
)

func row(name string, left, right *domain.SideMeta) *domain.DiffNode {
	return &domain.DiffNode{Name: name, Left: left, Right: right}
}

func meta(kind domain.EntryKind) *domain.SideMeta { return &domain.SideMeta{Kind: kind} }

func TestSortDiffChildren(t *testing.T) {
	t.Parallel()

	dir, file, link := meta(domain.KindDir), meta(domain.KindFile), meta(domain.KindSymlink)

	rows := []*domain.DiffNode{
		row("zeta.txt", file, file),
		row("beta", dir, dir),
		row("alpha.txt", file, nil), // removed file
		row("omega", nil, dir),      // added dir
		row("link", link, link),     // symlinks sort with the non-dirs
		row("was-dir", dir, file),   // type change: classified by the right side
		row("now-dir", file, dir),   // type change the other way
	}
	analyze.SortDiffChildren(rows)

	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Name)
	}
	assert.Equal(t, []string{
		"beta", "now-dir", "omega", // directories, name-ascending
		"alpha.txt", "link", "was-dir", "zeta.txt", // then the rest
	}, got)
}

func TestIsDirRow(t *testing.T) {
	t.Parallel()

	assert.True(t, analyze.IsDirRow(row("d", meta(domain.KindDir), meta(domain.KindDir))))
	assert.True(t, analyze.IsDirRow(row("added", nil, meta(domain.KindDir))),
		"an added directory is classified by the only side it has")
	assert.True(t, analyze.IsDirRow(row("removed", meta(domain.KindDir), nil)))
	assert.False(t, analyze.IsDirRow(row("became-file", meta(domain.KindDir), meta(domain.KindFile))),
		"the right (after) side wins when both exist")
	assert.False(t, analyze.IsDirRow(row("neither", nil, nil)))
}
