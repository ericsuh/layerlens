// Package tartest builds layer tars in memory for tests.
//
// Everything the indexer, the codec and (later) the squasher need to be
// exercised against is expressible here: regular files, directories, symlinks,
// hardlinks, devices, fifos, whiteout and opaque markers, PAX long names and
// xattrs, duplicate entries and deliberately hostile names — all with explicit
// control over the ordering of members, because tar order is load-bearing for
// several of the rules under test.
package tartest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/ericsuh/layerlens/internal/domain"
)

// DefaultMtime is the modification time every member gets unless a test
// overrides it, so that a digest difference is never an accident of the clock.
var DefaultMtime = time.Unix(1700000000, 0).UTC()

// Opt mutates a header before it is written.
type Opt func(*tar.Header)

// Mode sets the permission bits.
func Mode(m int64) Opt { return func(h *tar.Header) { h.Mode = m } }

// Owner sets uid and gid.
func Owner(uid, gid int) Opt { return func(h *tar.Header) { h.Uid, h.Gid = uid, gid } }

// Names sets uname and gname.
func Names(uname, gname string) Opt {
	return func(h *tar.Header) { h.Uname, h.Gname = uname, gname }
}

// Mtime sets the modification time.
func Mtime(t time.Time) Opt { return func(h *tar.Header) { h.ModTime = t } }

// Xattr adds one SCHILY.xattr.* PAX record.
func Xattr(name, value string) Opt {
	return func(h *tar.Header) {
		if h.PAXRecords == nil {
			h.PAXRecords = map[string]string{}
		}
		h.PAXRecords["SCHILY.xattr."+name] = value
		h.Format = tar.FormatPAX
	}
}

// PAX adds a raw PAX record, for records outside the xattr namespace.
func PAX(key, value string) Opt {
	return func(h *tar.Header) {
		if h.PAXRecords == nil {
			h.PAXRecords = map[string]string{}
		}
		h.PAXRecords[key] = value
		h.Format = tar.FormatPAX
	}
}

// Header applies an arbitrary mutation, for cases the named options do not
// cover.
func Header(fn func(*tar.Header)) Opt { return fn }

type member struct {
	hdr  tar.Header
	body string
}

// Builder accumulates members in the order they will appear in the tar.
type Builder struct {
	members []member
}

// New returns an empty Builder.
func New() *Builder { return &Builder{} }

func (b *Builder) add(h tar.Header, body string, opts []Opt) *Builder {
	if h.ModTime.IsZero() {
		h.ModTime = DefaultMtime
	}
	for _, o := range opts {
		o(&h)
	}
	b.members = append(b.members, member{hdr: h, body: body})
	return b
}

// File adds a regular file with the given content.
func (b *Builder) File(name, content string, opts ...Opt) *Builder {
	return b.add(tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(content)),
	}, content, opts)
}

// Dir adds a directory.
func (b *Builder) Dir(name string, opts ...Opt) *Builder {
	if len(name) == 0 || name[len(name)-1] != '/' {
		name += "/"
	}
	return b.add(tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: 0o755}, "", opts)
}

// Symlink adds a symbolic link pointing at target.
func (b *Builder) Symlink(name, target string, opts ...Opt) *Builder {
	return b.add(tar.Header{
		Typeflag: tar.TypeSymlink,
		Name:     name,
		Linkname: target,
		Mode:     0o777,
	}, "", opts)
}

// Hardlink adds a hard link to target. Tar hardlinks carry no content.
func (b *Builder) Hardlink(name, target string, opts ...Opt) *Builder {
	return b.add(tar.Header{
		Typeflag: tar.TypeLink,
		Name:     name,
		Linkname: target,
		Mode:     0o644,
	}, "", opts)
}

// Device adds a character (char true) or block device node.
func (b *Builder) Device(name string, char bool, major, minor int64, opts ...Opt) *Builder {
	flag := byte(tar.TypeBlock)
	if char {
		flag = tar.TypeChar
	}
	return b.add(tar.Header{
		Typeflag: flag,
		Name:     name,
		Mode:     0o600,
		Devmajor: major,
		Devminor: minor,
	}, "", opts)
}

// Fifo adds a named pipe.
func (b *Builder) Fifo(name string, opts ...Opt) *Builder {
	return b.add(tar.Header{Typeflag: tar.TypeFifo, Name: name, Mode: 0o600}, "", opts)
}

// Whiteout adds a ".wh."-prefixed marker deleting target from lower layers.
// target is the ordinary path of the file being deleted, e.g. "app/old.txt".
func (b *Builder) Whiteout(target string) *Builder {
	dir, base := splitLast(target)
	return b.add(tar.Header{Typeflag: tar.TypeReg, Name: dir + ".wh." + base, Mode: 0o644}, "", nil)
}

// Opaque adds a ".wh..wh..opq" marker inside dir.
func (b *Builder) Opaque(dir string) *Builder {
	if len(dir) > 0 && dir[len(dir)-1] != '/' {
		dir += "/"
	}
	return b.add(tar.Header{Typeflag: tar.TypeReg, Name: dir + ".wh..wh..opq", Mode: 0o644}, "", nil)
}

// Raw adds a member with a fully explicit header, for malformed or exotic
// cases.
func (b *Builder) Raw(h tar.Header, body string) *Builder {
	h.Size = int64(len(body))
	return b.add(h, body, nil)
}

func splitLast(p string) (dir, base string) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i+1], p[i+1:]
		}
	}
	return "", p
}

// Write streams the tar to w without buffering it.
func (b *Builder) Write(w io.Writer) error {
	tw := tar.NewWriter(w)
	for i := range b.members {
		m := &b.members[i]
		if err := tw.WriteHeader(&m.hdr); err != nil {
			return err
		}
		if m.body != "" {
			if _, err := io.WriteString(tw, m.body); err != nil {
				return err
			}
		}
	}
	return tw.Close()
}

// Bytes returns the uncompressed tar.
func (b *Builder) Bytes() []byte {
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Gzip returns the tar compressed with gzip.
func (b *Builder) Gzip() []byte { return Gzip(b.Bytes()) }

// Zstd returns the tar compressed with zstd.
func (b *Builder) Zstd() []byte { return Zstd(b.Bytes()) }

// DiffID is the digest of the uncompressed tar — what an image config would
// declare for this layer.
func (b *Builder) DiffID() domain.Digest { return DiffID(b.Bytes()) }

// Gzip compresses arbitrary bytes.
func Gzip(raw []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Zstd compresses arbitrary bytes.
func Zstd(raw []byte) []byte {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		panic(err)
	}
	if _, err := zw.Write(raw); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// DiffID returns the sha256 of an uncompressed tar.
func DiffID(raw []byte) domain.Digest {
	sum := sha256.Sum256(raw)
	return domain.DigestFromBytes(sum[:])
}

// SHA256 returns the digest of a string, for asserting on Entry.ContentSHA.
func SHA256(s string) domain.Digest {
	sum := sha256.Sum256([]byte(s))
	return domain.DigestFromBytes(sum[:])
}

// Pipe streams a tar produced by fn through an io.Pipe, so that a test can
// feed the indexer an arbitrarily large archive without ever holding it in
// memory. Errors from fn surface as read errors on the returned reader.
func Pipe(fn func(tw *tar.Writer) error) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		if err := fn(tw); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.CloseWithError(tw.Close())
	}()
	return pr
}

// Filler is an io.Reader that yields n bytes of a repeating pattern without
// allocating them, for synthesizing large file bodies.
type Filler struct {
	remaining int64
	pattern   byte
}

// NewFiller returns a reader of n bytes, all equal to pattern.
func NewFiller(n int64, pattern byte) *Filler { return &Filler{remaining: n, pattern: pattern} }

// Read implements io.Reader.
func (f *Filler) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > f.remaining {
		n = f.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = f.pattern
	}
	f.remaining -= n
	return int(n), nil
}
