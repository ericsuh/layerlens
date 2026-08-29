// Command genfixtures writes the vendored OCI image-layout fixtures that
// layerlens demos and tests against.
//
//	go run ./cmd/genfixtures --out fixtures
//
// The output is deterministic: regenerating over a committed fixtures/ tree
// must leave `git status` clean. That is the review guarantee behind RESEARCH
// Q2's "produced by a committed, deterministic generator … not opaque blobs" —
// the blobs are binary, but everything about them is derived from Go literals
// in cmd/genfixtures/gen, and anyone can re-derive them in a second.
//
// The generator never touches Docker or the network.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ericsuh/layerlens/cmd/genfixtures/gen"
)

func main() {
	out := flag.String("out", "fixtures", "directory to write the OCI image layouts into")
	quiet := flag.Bool("quiet", false, "suppress the per-pair summary")
	flag.Parse()

	if err := run(*out, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, "genfixtures:", err)
		os.Exit(1)
	}
}

func run(out string, quiet bool) error {
	reports, err := gen.Build(out)
	if err != nil {
		return err
	}
	if quiet {
		return nil
	}

	var total int64
	for _, r := range reports {
		total += r.Bytes
		fmt.Printf("%-9s %8s in %2d blobs  %s\n", r.Name, human(r.Bytes), r.BlobCount, r.Doc)
		for _, img := range r.Images {
			fmt.Printf("  %-18s %2d layers  %8s uncompressed  %8s stored  %s\n",
				img.Ref, len(img.DiffIDs), human(img.ContentBytes), human(img.BlobBytes), img.ID.Short())
		}
	}
	fmt.Printf("%-9s %8s on disk in %s\n", "TOTAL", human(total), out)
	return nil
}

// human renders a byte count the way the UI does, so the generator's own
// output and the app agree about how big a fixture is.
func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
