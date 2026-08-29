package gen

import "time"

// The two degenerate fork shapes of ARCHITECTURE §4.5. They are deliberately
// tiny: their whole purpose is to make the trunk arithmetic's boundary cases
// reachable from the UI and from e2e tests, not to look like real images.

var (
	prefixBuilt   = time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	disjointBuilt = time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
)

// prefixPair is the strict-prefix case: `prefix:base` is exactly the first
// three layers of `prefix:extended`, so the trunk covers the whole of the
// shorter image and its branch is empty. The UI has to render a fork with one
// empty side, and CouldBeSharedEdges must emit nothing at all.
func prefixPair() PairSpec {
	shared := []LayerSpec{
		{
			CreatedBy: "/bin/sh -c #(nop) ADD file:a1c0de… in / ",
			Mtime:     prefixBuilt,
			Entries: []EntrySpec{
				Dir("/bin"), Dir("/etc"), Dir("/usr"), Dir("/usr/bin"),
				File("/bin/busybox", 780*kib, WithMode(0o755)),
				File("/etc/os-release", 372),
				File("/etc/passwd", 340),
			},
		},
		{
			CreatedBy: "RUN /bin/sh -c apk add --no-cache ca-certificates # buildkit",
			Mtime:     prefixBuilt.Add(time.Minute),
			Entries: []EntrySpec{
				Dir("/etc/ssl"), Dir("/etc/ssl/certs"),
				File("/etc/ssl/certs/ca-certificates.crt", 214*kib),
				File("/usr/bin/update-ca-certificates", 3*kib, WithMode(0o755)),
			},
		},
		{
			CreatedBy: "COPY server /usr/bin/server # buildkit",
			Mtime:     prefixBuilt.Add(2 * time.Minute),
			Entries: []EntrySpec{
				File("/usr/bin/server", 6*mib, WithMode(0o755)),
			},
		},
	}
	extra := []LayerSpec{
		{
			CreatedBy: "COPY config/ /etc/server/ # buildkit",
			Mtime:     prefixBuilt.Add(3 * time.Minute),
			Entries: []EntrySpec{
				Dir("/etc/server"),
				File("/etc/server/server.yaml", 1842),
				File("/etc/server/logging.yaml", 640),
			},
		},
		{
			CreatedBy: `CMD ["/usr/bin/server" "--config" "/etc/server/server.yaml"]`,
			Empty:     true,
		},
		{
			CreatedBy: "RUN /bin/sh -c adduser -D -H app && chown app /etc/server # buildkit",
			Mtime:     prefixBuilt.Add(4 * time.Minute),
			Entries: []EntrySpec{
				File("/etc/passwd", 368, WithSeed("prefix/passwd@extended")),
				File("/etc/group", 210),
				Dir("/etc/server", WithOwner(1000, 1000)),
			},
		},
	}

	base := ImageSpec{
		Ref:     "prefix:base",
		Created: prefixBuilt,
		Env:     []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		Cmd:     []string{"/usr/bin/server"},
		Layers:  append(append([]LayerSpec{}, shared...), LayerSpec{CreatedBy: `CMD ["/usr/bin/server"]`, Empty: true}),
	}
	extended := ImageSpec{
		Ref:     "prefix:extended",
		Created: prefixBuilt.Add(4 * time.Minute),
		Env:     []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		Cmd:     []string{"/usr/bin/server", "--config", "/etc/server/server.yaml"},
		Layers:  append(append([]LayerSpec{}, shared...), extra...),
	}
	return PairSpec{
		Name:   "prefix",
		Doc:    "strict prefix: base is exactly the trunk of extended, so one branch is empty and there are no edges",
		Images: []ImageSpec{base, extended},
	}
}

// disjointPair shares nothing at all: different base, different everything, so
// the trunk length is 0 and both branches are whole images. Every layer of one
// is post-fork with respect to the other, which is the widest input
// CouldBeSharedEdges ever sees — and it must still emit nothing, because no
// changeset digest matches.
func disjointPair() PairSpec {
	a := ImageSpec{
		Ref:        "disjoint:a",
		Created:    disjointBuilt,
		Env:        []string{"PATH=/usr/local/bin:/usr/bin:/bin", "PYTHONUNBUFFERED=1"},
		Cmd:        []string{"python3", "/srv/app.py"},
		WorkingDir: "/srv",
		Layers: []LayerSpec{
			{
				CreatedBy: "/bin/sh -c #(nop) ADD file:b2d1ef… in / ",
				Mtime:     disjointBuilt,
				Entries: []EntrySpec{
					Dir("/usr"), Dir("/usr/bin"), Dir("/usr/lib"),
					File("/usr/bin/python3.12", 5*mib, WithMode(0o755)),
					File("/usr/lib/libpython3.12.so.1.0", 3*mib),
					File("/etc/os-release", 401),
				},
			},
			{
				CreatedBy: "COPY app.py /srv/app.py # buildkit",
				Mtime:     disjointBuilt.Add(time.Minute),
				Entries: []EntrySpec{
					Dir("/srv"),
					File("/srv/app.py", 3412),
					File("/srv/requirements.txt", 214),
				},
			},
		},
	}
	b := ImageSpec{
		Ref:        "disjoint:b",
		Created:    disjointBuilt.Add(time.Hour),
		Env:        []string{"PATH=/usr/local/bin:/usr/bin:/bin", "GOTRACEBACK=all"},
		Entrypoint: []string{"/usr/local/bin/worker"},
		Layers: []LayerSpec{
			{
				CreatedBy: "/bin/sh -c #(nop) ADD file:cc72fa… in / ",
				Mtime:     disjointBuilt.Add(time.Hour),
				Entries: []EntrySpec{
					Dir("/usr"), Dir("/usr/local"), Dir("/usr/local/bin"),
					File("/etc/nsswitch.conf", 494),
					File("/etc/ssl/certs/ca-certificates.crt", 208*kib),
				},
			},
			{
				CreatedBy: "COPY --from=build /out/worker /usr/local/bin/worker # buildkit",
				Mtime:     disjointBuilt.Add(time.Hour + time.Minute),
				Entries: []EntrySpec{
					File("/usr/local/bin/worker", 9*mib, WithMode(0o755)),
				},
			},
			{
				CreatedBy: "RUN /bin/sh -c mkdir -p /var/lib/worker # buildkit",
				Mtime:     disjointBuilt.Add(time.Hour + 2*time.Minute),
				Entries: []EntrySpec{
					Dir("/var"), Dir("/var/lib"), Dir("/var/lib/worker", WithOwner(65532, 65532)),
				},
			},
		},
	}
	return PairSpec{
		Name:   "disjoint",
		Doc:    "zero shared layers: trunk length 0, both branches are whole images, no edges",
		Images: []ImageSpec{a, b},
	}
}
