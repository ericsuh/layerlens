package gen

import "time"

// The edge-case pair concentrates every squash and diff rule that is easy to
// get wrong into two three-layer images (ARCHITECTURE §4.2, §9.2):
//
//   - an opaque directory marker that must clear the lower layers' children
//     while the directory node itself survives, and while the same layer's own
//     member inside that directory still lands;
//   - a dir -> file type change, which must drop the shadowed subtree;
//   - a file -> dir type change in the other direction;
//   - a symlink retargeted to a different path;
//   - a valid hardlink (bytes counted once, at the target) and a hardlink whose
//     target a later layer whiteouts, leaving it dangling;
//   - device nodes, a fifo and an xattr, so the non-file kinds are represented
//     in something the API and UI actually serve;
//   - and a **mode-only** change, which is the pair's sharpest assertion:
//     the file shows as *modified* in the tree, and because mode participates
//     in the tarsum-v1 changeset digest, the two layers do NOT get a
//     could-be-shared edge. Modified-but-not-shareable is a real state and the
//     fixture pins it.
var edgecaseBuilt = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

// edgecaseBaseLayer is shared by both images, so the trunk length is 1 and
// every marker in layer 2 has real lower state to act on.
func edgecaseBaseLayer() LayerSpec {
	return LayerSpec{
		CreatedBy: "/bin/sh -c #(nop) ADD file:e0f1a2… in / ",
		Mtime:     edgecaseBuilt,
		Entries: []EntrySpec{
			Dir("/data"), Dir("/data/cache"), Dir("/etc"), Dir("/etc/config"),
			Dir("/opt"), Dir("/opt/tool"), Dir("/run"), Dir("/srv"),
			Dir("/usr"), Dir("/usr/bin"), Dir("/dev"),

			// Lower children an opaque marker will have to hide.
			File("/data/cache/a.bin", 4*kib),
			File("/data/cache/b.bin", 6*kib),
			File("/data/cache/c.bin", 8*kib),
			File("/data/keep.txt", 512),

			// A directory that one image later replaces with a file.
			File("/etc/config/base.conf", 780),
			File("/etc/config/extra.conf", 240),

			// A file that one image later replaces with a directory.
			File("/opt/tool/plugins", 96),
			File("/opt/tool/bin.sh", 1024, WithMode(0o755)),
			File("/opt/tool/lib.so", 48*kib),
			Symlink("/opt/current", "/opt/tool"),

			// Hardlinks: one that stays valid, one whose target is
			// whiteouted below.
			File("/usr/bin/real", 4096, WithMode(0o755)),
			Hardlink("/usr/bin/alias", "/usr/bin/real", WithMode(0o755)),
			File("/srv/dangle-target.txt", 2048),
			Hardlink("/srv/dangle", "/srv/dangle-target.txt"),

			// Kinds that are not files: they must survive indexing,
			// squashing and diffing without contributing bytes.
			Device("/dev/null", true, 1, 3, WithMode(0o666)),
			Device("/dev/loop0", false, 7, 0, WithMode(0o660)),
			Fifo("/run/initctl"),
			File("/usr/bin/ping", 76*kib, WithMode(0o755),
				WithXattr("security.capability", "\x01\x00\x00\x02\x00 \x00\x00")),
			// Non-root ownership, which the changeset digest includes
			// (RESEARCH Q9) and the tree shows.
			File("/srv/owned.txt", 128, WithOwner(1000, 1000), WithNames("app", "app")),

			// The mode-only subject. Both images restate it in layer 3
			// with identical content; only the permission bits differ.
			File("/srv/mode.sh", 640, WithMode(0o644)),
		},
	}
}

// edgecaseModeLayer is the third layer of both images. Its entries are
// identical apart from the permission bits of /srv/mode.sh, so the tree must
// report the file as modified while the edge computation must stay silent.
func edgecaseModeLayer(mode uint32) LayerSpec {
	return LayerSpec{
		CreatedBy: "RUN /bin/sh -c install -m " + modeString(mode) + " mode.sh /srv/mode.sh # buildkit",
		Mtime:     edgecaseBuilt.Add(2 * time.Hour),
		Entries: []EntrySpec{
			File("/srv/mode.sh", 640, WithMode(mode)),
			File("/srv/release.txt", 64),
		},
	}
}

func modeString(mode uint32) string {
	const digits = "01234567"
	return string([]byte{
		digits[(mode>>9)&7], digits[(mode>>6)&7], digits[(mode>>3)&7], digits[mode&7],
	})
}

// edgecasePair returns `edgecase:opaque` / `edgecase:plain`.
func edgecasePair() PairSpec {
	opaque := ImageSpec{
		Ref:     "edgecase:opaque",
		Created: edgecaseBuilt.Add(2 * time.Hour),
		Env:     []string{"PATH=/usr/local/bin:/usr/bin:/bin"},
		Layers: []LayerSpec{
			edgecaseBaseLayer(),
			{
				CreatedBy: "RUN /bin/sh -c rm -rf /data/cache/* /etc/config /srv/dangle-target.txt && printf '' > /etc/config && ln -sfn /opt/tool-v2 /opt/current # buildkit",
				Mtime:     edgecaseBuilt.Add(time.Hour),
				Entries: []EntrySpec{
					// Opaque marker plus this layer's own member in
					// the same directory: the marker must hide the
					// three lower .bin files and new.bin must
					// survive, regardless of tar order.
					Opaque("/data/cache"),
					File("/data/cache/new.bin", 2*kib),

					// dir -> file: the whole /etc/config subtree
					// is shadowed by a regular file.
					File("/etc/config", 128),

					// file -> dir: /opt/tool/plugins was a file.
					Dir("/opt/tool/plugins"),
					File("/opt/tool/plugins/codec.so", 12*kib),

					// Symlink retargeted at a directory this layer
					// creates.
					Symlink("/opt/current", "/opt/tool-v2"),
					Dir("/opt/tool-v2"),
					File("/opt/tool-v2/bin.sh", 1200, WithMode(0o755)),

					// Leaves /srv/dangle pointing at nothing.
					Whiteout("/srv/dangle-target.txt"),
				},
			},
			edgecaseModeLayer(0o755),
		},
	}
	plain := ImageSpec{
		Ref:     "edgecase:plain",
		Created: edgecaseBuilt.Add(2 * time.Hour),
		Env:     []string{"PATH=/usr/local/bin:/usr/bin:/bin"},
		Layers: []LayerSpec{
			edgecaseBaseLayer(),
			{
				CreatedBy: "RUN /bin/sh -c cp new.bin /data/cache/ && cp codec.so /opt/tool/ # buildkit",
				Mtime:     edgecaseBuilt.Add(time.Hour),
				Entries: []EntrySpec{
					// Same file, no marker: the lower cache
					// entries stay, which is what makes the
					// opaque image's disappearance visible in a
					// diff.
					File("/data/cache/new.bin", 2*kib),
					File("/etc/config/extra.conf", 240),
					File("/opt/tool/codec.so", 12*kib),
				},
			},
			edgecaseModeLayer(0o644),
		},
	}
	return PairSpec{
		Name:   "edgecase",
		Doc:    "opaque dir, dir<->file type changes, symlink retarget, dangling hardlink, devices/fifo/xattrs, and a mode-only change that is modified in the tree but produces no edge",
		Images: []ImageSpec{opaque, plain},
	}
}
