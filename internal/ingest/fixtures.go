package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/ericsuh/layerlens/internal/domain"
)

// FixtureResult summarizes a startup fixture load.
type FixtureResult struct {
	// Layouts is the number of OCI layout directories that were read.
	Layouts int
	// Ingested is the number of images analyzed during this load.
	Ingested int
	// AlreadyPresent is the number of images the durable cache already
	// held — the number that makes a restart cheap.
	AlreadyPresent int
	// LayersIndexed and LayersSkipped are summed over the ingested images.
	LayersIndexed int
	LayersSkipped int
	// Images are the records now in the cache, fixtures only.
	Images  []domain.ImageRecord
	Elapsed time.Duration
}

// LoadFixtures ingests every image in every OCI layout under dir and pins it.
//
// This is what makes the demo work with no network and no Docker daemon: the
// images ship in the repository, and a restart re-reads them from the durable
// cache instead of re-analyzing them (RESEARCH Q2).
//
// Pinning is not a nicety. The fixtures are the application's entire offline
// story, so letting an LRU sweep triggered by a large pull delete them would
// break the demo at exactly the moment the cache is under pressure.
func (i *Ingester) LoadFixtures(ctx context.Context, dir string) (*FixtureResult, error) {
	start := time.Now()
	dirs, err := DiscoverLayouts(dir)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("%w under %s", ErrNoLayouts, dir)
	}

	res := &FixtureResult{Layouts: len(dirs)}
	for _, layoutDir := range dirs {
		images, err := OpenLayout(layoutDir)
		if err != nil {
			return nil, err
		}
		for _, img := range images {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			meta := Meta{
				Source:         domain.SourceFixture,
				Pinned:         true,
				ManifestDigest: img.ManifestDigest,
			}
			if img.Ref != "" {
				meta.RefNames = []string{img.Ref}
			}
			one, err := i.Ingest(ctx, img.Image, meta)
			if err != nil {
				return nil, fmt.Errorf("ingest: fixture %s in %s: %w", img.Ref, layoutDir, err)
			}
			if one.AlreadyPresent {
				res.AlreadyPresent++
			} else {
				res.Ingested++
				res.LayersIndexed += one.LayersIndexed
				res.LayersSkipped += one.LayersSkipped
				i.log.Info("ingest: analyzed fixture image",
					"ref", img.Ref, "id", one.Record.ID.Short(),
					"layersIndexed", one.LayersIndexed,
					"layersSkipped", one.LayersSkipped,
					"totalBytes", one.Record.TotalBytes)
			}
			res.Images = append(res.Images, *one.Record)
		}
	}
	res.Elapsed = time.Since(start)
	i.log.Info("ingest: fixtures loaded",
		"layouts", res.Layouts, "ingested", res.Ingested,
		"alreadyCached", res.AlreadyPresent, "elapsed", res.Elapsed)
	return res, nil
}
