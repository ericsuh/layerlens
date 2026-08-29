// Not a real Go module: nothing here is built or imported.
//
// This file exists only so that `go test ./...` and `golangci-lint run` from
// the repository root stop at a module boundary and never descend into
// web/node_modules, where npm dependencies ship Go sources of their own
// (for example flatted/golang/pkg/flatted).
module github.com/ericsuh/layerlens/web

go 1.26.0
