package gen

import "time"

// WideDirChildren is the number of entries in the wide pair's fat directory.
// The tree API pages at 500 rows, so 2500 children force exactly five pages
// and make "load the next page" a thing an e2e test can observe rather than
// assume (ARCHITECTURE §9.2, §9.4).
const WideDirChildren = 2500

// WideDirPath is the directory that holds them.
const WideDirPath = "/data/shards"

var wideBuilt = time.Date(2026, 7, 28, 6, 30, 0, 0, time.UTC)

// wideModified lists the shard indexes whose content differs between v1 and
// v2. It is small on purpose: the point of the pair is pagination, and a
// handful of changes deep inside a 2500-row directory is also the worst case
// for the "changed only" filter, which has to page through the unchanged bulk
// to find them.
var wideModified = []int{7, 493, 1000, 1777, 2499}

// widePair returns `wide:v1` / `wide:v2`: one shared layer holding a directory
// with WideDirChildren tiny files, and a second layer that rewrites five of
// them. The child count is identical on both sides so the pagination assertion
// has a single, exact expected value.
func widePair() PairSpec {
	base := LayerSpec{
		CreatedBy: "COPY shards/ " + WideDirPath + "/ # buildkit",
		Mtime:     wideBuilt,
		Entries:   wideBaseEntries(),
	}
	image := func(ref, variant string, created time.Time, layers []LayerSpec) ImageSpec {
		return ImageSpec{
			Ref:        ref,
			Created:    created,
			Env:        []string{"PATH=/usr/local/bin:/usr/bin:/bin", "SHARD_SET=" + variant},
			WorkingDir: "/data",
			Layers:     layers,
		}
	}

	v1 := image("wide:v1", "v1", wideBuilt, []LayerSpec{base})
	v2 := image("wide:v2", "v2", wideBuilt.Add(24*time.Hour), []LayerSpec{
		base,
		{
			CreatedBy: "COPY shards-patch/ " + WideDirPath + "/ # buildkit",
			Mtime:     wideBuilt.Add(24 * time.Hour),
			Entries:   widePatchEntries(),
		},
	})
	return PairSpec{
		Name:   "wide",
		Doc:    "one directory with 2500 children for tree pagination; v2 rewrites five of them",
		Images: []ImageSpec{v1, v2},
	}
}

func wideBaseEntries() []EntrySpec {
	es := make([]EntrySpec, 0, WideDirChildren+2)
	es = append(es, Dir("/data"), Dir(WideDirPath))
	for i := range WideDirChildren {
		p := wideShardPath(i)
		es = append(es, File(p, spread(p, 96, 320)))
	}
	return es
}

func widePatchEntries() []EntrySpec {
	es := make([]EntrySpec, 0, len(wideModified))
	for _, i := range wideModified {
		p := wideShardPath(i)
		// Same path, same size class, different seed: the file is
		// modified, and the directory's child count does not move.
		es = append(es, File(p, spread(p, 96, 320)+16, WithSeed(p+"@v2")))
	}
	return es
}

func wideShardPath(i int) string { return numbered(WideDirPath+"/shard-%04d.dat", i) }
