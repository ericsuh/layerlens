package gen

import (
	"time"
)

// The golden demo pair: two builds of PROJECT.md's Dockerfile
//
//	FROM node:24.04
//	WORKDIR /app
//	COPY . .
//	RUN npm install
//	RUN apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/
//	CMD ["node", "./main.js"]
//
// where the second build's context carries junk a `.dockerignore` should have
// excluded (a `.git/` directory, a `debug.log`, a `.env`). That junk lands in
// the `COPY . .` layer, which forks the two images, which in turn invalidates
// the `RUN npm install` layer below it — even though `npm install` produced
// byte-for-byte the same node_modules both times.
//
// The fixture encodes that lesson as three checkable facts:
//
//  1. the five trunk layers are identical, so TrunkLCP is exactly 5;
//  2. the two npm layers ship identical *content* with different mtimes, so
//     their DiffIDs differ while their normalized changeset digests match —
//     a could-be-shared edge with DiffIDEqual == false. This is the crux: it
//     is the machine-checkable form of "a `.dockerignore` would have saved
//     you this layer";
//  3. the two ffmpeg layers are byte-identical, so their edge has
//     DiffIDEqual == true — the contrasting case, and proof that the UI's two
//     edge flavours both occur in the demo.
//
// Fact 2 is asserted in gen_test.go; if it ever stops holding, the demo stops
// making its point, so the test is the guard rather than this comment.

// Build timestamps of the two example builds. Their difference is the mtime
// skew that fact 2 above rests on.
var (
	exampleV1Built = time.Date(2026, 8, 27, 14, 2, 0, 0, time.UTC)
	exampleV2Built = time.Date(2026, 8, 29, 9, 41, 0, 0, time.UTC)
)

// Trunk layer timestamps. They are fixed and shared by both builds because
// that is what actually happens: the second build hits the layer cache for
// every step above `COPY . .`, so it reuses the *first* build's layers, mtimes
// and all. A trunk layer whose mtime tracked the build clock would not be a
// trunk layer.
var (
	exampleBaseMtime  = time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	exampleAptMtime   = time.Date(2026, 5, 18, 11, 20, 0, 0, time.UTC)
	exampleNodeMtime  = time.Date(2026, 6, 2, 8, 30, 0, 0, time.UTC)
	exampleYarnMtime  = time.Date(2026, 6, 2, 8, 31, 0, 0, time.UTC)
	exampleWorkMtime  = time.Date(2026, 6, 2, 8, 32, 0, 0, time.UTC)
	exampleFfmpegTime = time.Date(2026, 4, 9, 7, 15, 0, 0, time.UTC)
)

// npm-install layer timestamps: the same files, written a build apart. This is
// the deliberate skew, and it lives in the *entry* mtimes inside the tar (not
// in the gzip header), because only entry mtimes change the DiffID while
// leaving the tarsum-v1 changeset digest alone.
var (
	exampleNpmMtimeV1 = time.Date(2026, 8, 27, 14, 2, 44, 0, time.UTC)
	exampleNpmMtimeV2 = time.Date(2026, 8, 29, 9, 41, 52, 0, time.UTC)
)

// Verbatim `created_by` strings, in the shapes real builders emit: the classic
// builder's "#(nop)" metadata lines for the base image and BuildKit's
// "# buildkit"-suffixed lines for the RUN steps. analyze.CleanInstruction has
// to strip both, so the fixtures exercise both.
const (
	cbBaseAdd  = "/bin/sh -c #(nop) ADD file:4d192565a7220e135cab8c6c9d3e6f2f8e4b5a6c7d8e9f0a1b2c3d4e5f6a7b8 in / "
	cbBaseCmd  = `/bin/sh -c #(nop)  CMD ["bash"]`
	cbAptDeps  = "RUN /bin/sh -c set -eux; apt-get update; apt-get install -y --no-install-recommends ca-certificates curl git wget gnupg dirmngr xz-utils libatomic1 # buildkit"
	cbNodeVer  = "ENV NODE_VERSION=24.4.0"
	cbNodeInst = `RUN /bin/sh -c ARCH= && dpkgArch="$(dpkg --print-architecture)" && curl -fsSLO --compressed "https://nodejs.org/dist/v24.4.0/node-v24.4.0-linux-x64.tar.xz" && tar -xJf "node-v24.4.0-linux-x64.tar.xz" -C /usr/local --strip-components=1 --no-same-owner && ln -s /usr/local/bin/node /usr/local/bin/nodejs # buildkit`
	cbYarnVer  = "ENV YARN_VERSION=1.22.22"
	cbYarnInst = `RUN /bin/sh -c set -ex && curl -fsSLO --compressed "https://yarnpkg.com/downloads/1.22.22/yarn-v1.22.22.tar.gz" && mkdir -p /opt && tar -xzf yarn-v1.22.22.tar.gz -C /opt/ && ln -s /opt/yarn-v1.22.22/bin/yarn /usr/local/bin/yarn # buildkit`
	cbWorkdir  = "WORKDIR /app"
	cbCopy     = "COPY . . # buildkit"
	cbNpm      = "RUN /bin/sh -c npm install # buildkit"
	cbFfmpeg   = "RUN /bin/sh -c apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/ # buildkit"
	cbCmd      = `CMD ["node" "./main.js"]`
)

// exampleEnv is the runtime environment a node:24 image carries.
var exampleEnv = []string{
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	"NODE_VERSION=24.4.0",
	"YARN_VERSION=1.22.22",
}

// examplePair returns the `example:v1` / `example:v2` layout spec.
func examplePair() PairSpec {
	trunk := func() []LayerSpec {
		return []LayerSpec{
			{CreatedBy: cbBaseAdd, Mtime: exampleBaseMtime, Entries: baseRootfsEntries()},
			{CreatedBy: cbBaseCmd, Empty: true},
			{CreatedBy: cbAptDeps, Mtime: exampleAptMtime, Entries: aptDepsEntries()},
			{CreatedBy: cbNodeVer, Empty: true},
			{CreatedBy: cbNodeInst, Mtime: exampleNodeMtime, Entries: nodeRuntimeEntries()},
			{CreatedBy: cbYarnVer, Empty: true},
			{CreatedBy: cbYarnInst, Mtime: exampleYarnMtime, Entries: yarnEntries()},
			// WORKDIR materializes /app, so BuildKit records it as a
			// real (one-entry, zero-byte) layer rather than an
			// empty_layer history line.
			{CreatedBy: cbWorkdir, Mtime: exampleWorkMtime, Entries: []EntrySpec{Dir("/app")}},
		}
	}

	image := func(ref string, built time.Time, copyEntries []EntrySpec, npmMtime time.Time) ImageSpec {
		layers := trunk()
		layers = append(layers,
			LayerSpec{CreatedBy: cbCopy, Mtime: built.Add(3 * time.Second), Entries: copyEntries},
			LayerSpec{CreatedBy: cbNpm, Mtime: npmMtime, Entries: npmInstallEntries()},
			LayerSpec{CreatedBy: cbFfmpeg, Mtime: exampleFfmpegTime, Entries: ffmpegEntries()},
			LayerSpec{CreatedBy: cbCmd, Empty: true},
		)
		return ImageSpec{
			Ref:        ref,
			Created:    built,
			Env:        exampleEnv,
			Cmd:        []string{"node", "./main.js"},
			WorkingDir: "/app",
			Layers:     layers,
		}
	}

	return PairSpec{
		Name: "example",
		Doc:  "golden demo: identical trunk, diverging COPY (.dockerignore junk in v2), content-identical npm layers with skewed mtimes, ffmpeg layer with rm -rf whiteouts",
		Images: []ImageSpec{
			image("example:v1", exampleV1Built, copyLayerV1(), exampleNpmMtimeV1),
			image("example:v2", exampleV2Built, copyLayerV2(), exampleNpmMtimeV2),
		},
	}
}

// ---------------------------------------------------------------------------
// Trunk layer 1 — the Debian bookworm base rootfs.
// ---------------------------------------------------------------------------

func baseRootfsEntries() []EntrySpec {
	es := []EntrySpec{
		Dir("/bin"), Dir("/etc"), Dir("/etc/apt"), Dir("/etc/ssl"), Dir("/opt"),
		Dir("/root", WithMode(0o700)), Dir("/tmp", WithMode(0o1777)),
		Dir("/usr"), Dir("/usr/bin"), Dir("/usr/lib"), Dir("/usr/lib/x86_64-linux-gnu"),
		Dir("/usr/lib/locale"), Dir("/usr/lib/locale/C.utf8"),
		Dir("/usr/share"), Dir("/usr/share/doc"), Dir("/usr/share/zoneinfo"),
		Dir("/var"), Dir("/var/lib"),
		// The four directories the Dockerfile's final `rm -rf
		// /var/lib/{apt,dpkg,cache,log}/` targets. The brace expansion
		// is literal — it names /var/lib/cache and /var/lib/log, not
		// /var/cache and /var/log — so the fixture ships all four and
		// every whiteout in the ffmpeg layer deletes something real.
		Dir("/var/lib/apt"), Dir("/var/lib/apt/lists"),
		Dir("/var/lib/dpkg"), Dir("/var/lib/dpkg/info"),
		Dir("/var/lib/cache"), Dir("/var/lib/log"),
	}

	for _, t := range []struct {
		name string
		size int64
	}{
		{"bash", 316 * kib}, {"ls", 36 * kib}, {"cat", 11 * kib}, {"cp", 38 * kib},
		{"mv", 37 * kib}, {"rm", 18 * kib}, {"sh", 32 * kib}, {"grep", 50 * kib},
		{"sed", 28 * kib}, {"tar", 148 * kib}, {"gzip", 25 * kib}, {"chmod", 16 * kib},
		{"mkdir", 22 * kib}, {"dd", 36 * kib}, {"ln", 19 * kib},
	} {
		es = append(es, File("/bin/"+t.name, t.size, WithMode(0o755)))
	}
	for _, n := range []string{"awk", "find", "xargs", "sort", "head", "tail", "cut", "tr", "du", "df", "env", "id", "uname", "wc", "which"} {
		es = append(es, File("/usr/bin/"+n, spread("bin/"+n, 8*kib, 76*kib), WithMode(0o755)))
	}

	for _, t := range []struct {
		name string
		size int64
	}{
		{"libc.so.6", 480 * kib}, {"libm.so.6", 226 * kib},
		{"libstdc++.so.6.0.30", 565 * kib}, {"libssl.so.3", 168 * kib},
		{"libcrypto.so.3", 1113 * kib}, {"libz.so.1.2.13", 30 * kib},
	} {
		es = append(es, File("/usr/lib/x86_64-linux-gnu/"+t.name, t.size, WithMode(0o644)))
	}
	for _, n := range []string{
		"pcre2-8", "selinux", "acl", "attr", "blkid", "mount", "smartcols", "uuid",
		"tinfo", "gcrypt", "gpg-error", "lz4", "lzma", "zstd", "systemd-shared",
		"apt-pkg", "apt-private", "dpkg", "gmp", "hogweed", "nettle", "tasn1",
		"unistring", "idn2", "p11-kit", "ffi", "seccomp", "cap-ng", "audit", "pam",
		"crypt", "db-5.3", "bz2", "expat",
	} {
		p := "/usr/lib/x86_64-linux-gnu/lib" + n + ".so"
		es = append(es, File(p, spread(p, 15*kib, 400*kib), WithMode(0o644)))
	}

	for _, t := range []struct {
		name string
		size int64
	}{
		{"passwd", 2968}, {"group", 1228}, {"shadow", 1434}, {"hostname", 12},
		{"os-release", 409}, {"resolv.conf", 102}, {"fstab", 717}, {"profile", 819},
		{"bash.bashrc", 2252},
	} {
		mode := uint32(0o644)
		if t.name == "shadow" {
			mode = 0o640
		}
		es = append(es, File("/etc/"+t.name, t.size, WithMode(mode)))
	}
	es = append(es, File("/etc/apt/sources.list", 342))

	// apt/dpkg state — the bulk of what the final `rm -rf` reclaims.
	es = append(es,
		File("/var/lib/dpkg/status", 296*kib),
		File("/var/lib/dpkg/available", 53*kib),
		File("/var/lib/apt/lists/deb.debian.org_debian_dists_bookworm_InRelease", 132*kib),
		File("/var/lib/apt/lists/deb.debian.org_debian_dists_bookworm_main_binary-amd64_Packages", 1088*kib),
		File("/var/lib/apt/lists/lock", 0, WithMode(0o640)),
		File("/var/lib/cache/debconf/config.dat", 18*kib),
		File("/var/lib/cache/debconf/templates.dat", 42*kib),
		File("/var/lib/log/dpkg.log", 9*kib),
		File("/var/lib/log/alternatives.log", 3*kib),
	)
	for i := range 22 {
		p := numbered("/var/lib/dpkg/info/pkg%02d.list", i)
		es = append(es, File(p, spread(p, 500, 6*kib)))
	}

	for i := range 18 {
		p := numbered("/usr/share/doc/pkg%02d/copyright", i)
		es = append(es, File(p, spread(p, kib, 8*kib)))
	}
	es = append(es, File("/usr/share/zoneinfo/UTC", 118))
	for i := range 30 {
		p := numbered("/usr/share/zoneinfo/zone%02d", i)
		es = append(es, File(p, spread(p, 200, 3*kib)))
	}

	// Locale data: one big, boring, highly compressible block that gives
	// the base layer the heft a real base image has.
	for i := range 8 {
		p := numbered("/usr/lib/locale/C.utf8/part%02d", i)
		es = append(es, File(p, spread(p, 400*kib, 900*kib)))
	}
	return es
}

// ---------------------------------------------------------------------------
// Trunk layer 2 — curl/git/ca-certificates.
// ---------------------------------------------------------------------------

func aptDepsEntries() []EntrySpec {
	es := []EntrySpec{
		Dir("/etc/ssl/certs"), Dir("/usr/lib/git-core"),
		Dir("/usr/share/git-core"), Dir("/usr/share/git-core/templates"),
		Dir("/usr/share/git-core/templates/hooks"), Dir("/usr/share/gnupg"),
		File("/usr/bin/curl", 65*kib, WithMode(0o755)),
		File("/usr/bin/git", 997*kib, WithMode(0o755)),
		File("/usr/bin/wget", 137*kib, WithMode(0o755)),
		File("/usr/bin/xz", 22*kib, WithMode(0o755)),
		File("/usr/bin/gpg", 276*kib, WithMode(0o755)),
	}
	for _, n := range []string{
		"remote-https", "http-fetch", "daemon", "shell", "upload-pack",
		"receive-pack", "credential-store", "imap-send", "sh-i18n",
		"fast-import", "http-backend", "web--browse",
	} {
		p := "/usr/lib/git-core/git-" + n
		es = append(es, File(p, spread(p, 220*kib, 980*kib), WithMode(0o755)))
	}
	for i := range 58 {
		p := numbered("/etc/ssl/certs/cert-%03d.pem", i)
		es = append(es, File(p, spread(p, 1200, 4200)))
	}
	es = append(es, File("/etc/ssl/certs/ca-certificates.crt", 214*kib))
	for _, n := range []string{"curl", "nghttp2", "ssh2", "psl", "brotlidec", "brotlicommon", "ldap", "sasl2"} {
		p := "/usr/lib/x86_64-linux-gnu/lib" + n + ".so"
		es = append(es, File(p, spread(p, 30*kib, 195*kib)))
	}
	for i := range 9 {
		p := numbered("/usr/share/git-core/templates/hooks/hook%d.sample", i)
		es = append(es, File(p, spread(p, 400, 4*kib), WithMode(0o755)))
	}
	for i := range 6 {
		p := numbered("/usr/share/gnupg/keyring%d.gpg", i)
		es = append(es, File(p, spread(p, 600*kib, 1550*kib)))
	}
	return es
}

// ---------------------------------------------------------------------------
// Trunk layer 3 — the node 24 runtime.
// ---------------------------------------------------------------------------

func nodeRuntimeEntries() []EntrySpec {
	es := []EntrySpec{
		Dir("/usr/local"), Dir("/usr/local/bin"), Dir("/usr/local/include"),
		Dir("/usr/local/include/node"), Dir("/usr/local/lib"),
		Dir("/usr/local/lib/node_modules"), Dir("/usr/local/lib/node_modules/npm"),
		Dir("/usr/local/lib/node_modules/npm/bin"),
		Dir("/usr/local/lib/node_modules/npm/lib"),
		Dir("/usr/local/lib/node_modules/npm/lib/commands"),
		Dir("/usr/local/lib/node_modules/npm/node_modules"),
		Dir("/usr/local/share"), Dir("/usr/local/share/man"), Dir("/usr/local/share/man/man1"),
		// The single fattest file in the image, as in a real node image.
		File("/usr/local/bin/node", 14*mib, WithMode(0o755)),
		// The runtime's own symlinks — squashing and diffing must carry
		// link targets, not resolve them.
		Symlink("/usr/local/bin/nodejs", "/usr/local/bin/node"),
		Symlink("/usr/local/bin/npm", "../lib/node_modules/npm/bin/npm-cli.js"),
		Symlink("/usr/local/bin/npx", "../lib/node_modules/npm/bin/npx-cli.js"),
		File("/usr/local/lib/node_modules/npm/bin/npm-cli.js", 54, WithMode(0o755)),
		File("/usr/local/lib/node_modules/npm/index.js", 4532),
		File("/usr/local/lib/node_modules/npm/package.json", 8843),
	}
	for _, h := range []string{
		"node.h", "node_api.h", "node_buffer.h", "node_version.h", "v8.h",
		"v8-array-buffer.h", "v8-context.h", "v8-data.h", "v8-exception.h",
		"v8-function.h", "v8-internal.h", "v8-isolate.h", "v8-json.h",
		"v8-local-handle.h", "v8-maybe.h", "v8-message.h", "v8-object.h",
		"v8-primitive.h", "v8-promise.h", "v8-script.h", "v8-template.h",
		"v8-value.h", "v8-version.h", "uv.h", "zlib.h",
	} {
		p := "/usr/local/include/node/" + h
		es = append(es, File(p, spread(p, 3*kib, 52*kib)))
	}
	for _, pk := range []string{
		"semver", "glob", "minimatch", "which", "ini", "tar", "pacote", "cacache",
		"make-fetch-happen", "npm-package-arg", "abbrev", "chalk", "ci-info",
		"cli-table3", "hosted-git-info", "minipass", "node-gyp", "nopt",
		"normalize-package-data", "proc-log", "read", "ssri", "treeverse",
		"validate-npm-package-name", "write-file-atomic", "graceful-fs",
	} {
		b := "/usr/local/lib/node_modules/npm/node_modules/" + pk
		es = append(es,
			Dir(b),
			File(b+"/package.json", spread(b+"pkg", 600, 2500)),
			File(b+"/index.js", spread(b+"idx", 2*kib, 24*kib)),
			File(b+"/README.md", spread(b+"rd", kib, 12*kib)),
		)
	}
	for i := range 10 {
		p := numbered("/usr/local/lib/node_modules/npm/lib/commands/cmd%02d.js", i)
		es = append(es, File(p, spread(p, 4*kib, 40*kib)))
	}
	for i := range 12 {
		p := numbered("/usr/local/share/man/man1/node-%02d.1", i)
		es = append(es, File(p, spread(p, kib, 9*kib)))
	}
	return es
}

// ---------------------------------------------------------------------------
// Trunk layer 4 — corepack/yarn.
// ---------------------------------------------------------------------------

func yarnEntries() []EntrySpec {
	return []EntrySpec{
		Dir("/opt/yarn-v1.22.22"), Dir("/opt/yarn-v1.22.22/bin"),
		File("/opt/yarn-v1.22.22/bin/yarn.js", 654*kib, WithMode(0o755)),
		File("/opt/yarn-v1.22.22/bin/yarn", 1225, WithMode(0o755)),
		File("/opt/yarn-v1.22.22/package.json", 2145),
		Symlink("/usr/local/bin/yarn", "/opt/yarn-v1.22.22/bin/yarn"),
		Symlink("/usr/local/bin/yarnpkg", "/opt/yarn-v1.22.22/bin/yarn"),
	}
}

// ---------------------------------------------------------------------------
// The fork: COPY . .
// ---------------------------------------------------------------------------

// appSources are the files both builds legitimately copy. main.js and
// src/util.js were edited between the two builds, so they carry an explicit
// seed and a different size — those two show as *modified* in the tree while
// everything else beside them stays unchanged.
func appSources(variant string, mainSize, utilSize int64) []EntrySpec {
	return []EntrySpec{
		Dir("/app/public"), Dir("/app/src"), Dir("/app/views"),
		File("/app/main.js", mainSize, WithSeed("app/main.js@"+variant)),
		File("/app/package.json", 1184),
		File("/app/package-lock.json", 182*kib),
		File("/app/src/index.js", 5240),
		File("/app/src/routes.js", 8931),
		File("/app/src/util.js", utilSize, WithSeed("app/src/util.js@"+variant)),
		File("/app/src/render.js", 3712),
		File("/app/views/home.ejs", 2210),
		File("/app/views/clip.ejs", 1874),
		File("/app/public/style.css", 6120),
		File("/app/public/app.js", 4488),
	}
}

// copyLayerV1 is the first build's context: clean, and still shipping the
// legacy helpers the second build deleted.
func copyLayerV1() []EntrySpec {
	return append(appSources("v1", 4212, 2966),
		File("/app/src/old-util.js", 1810),
		Dir("/app/src/legacy"),
		File("/app/src/legacy/shim.js", 940),
		File("/app/src/legacy/polyfill.js", 1520),
	)
}

// copyLayerV2 is the second build's context: the same application plus
// everything a missing `.dockerignore` let through. This is the entire lesson
// of the demo, and it is why the npm layer below it had to be rebuilt.
func copyLayerV2() []EntrySpec {
	es := append(appSources("v2", 4907, 3105),
		// Secrets and noise that should never have entered an image.
		File("/app/.env", 412, WithMode(0o600)),
		File("/app/debug.log", 158*kib),
		// ... and the whole repository history, which is both the
		// biggest single addition and the most obviously wrong one.
		Dir("/app/.git"),
		Dir("/app/.git/objects"), Dir("/app/.git/objects/pack"),
		Dir("/app/.git/logs"), Dir("/app/.git/logs/refs"), Dir("/app/.git/logs/refs/heads"),
		File("/app/.git/HEAD", 23),
		File("/app/.git/config", 310),
		File("/app/.git/index", 20488),
		File("/app/.git/packed-refs", 1024),
		File("/app/.git/COMMIT_EDITMSG", 64),
		File("/app/.git/objects/pack/pack-8c4f21ab.pack", 4552*kib),
		File("/app/.git/objects/pack/pack-8c4f21ab.idx", 50*kib),
	)
	// Loose objects, deliberately without explicit directory members: the
	// two-character fan-out directories arrive as implicit nodes, which is
	// what a real .git tar does and what the squasher must synthesize.
	for i := range 40 {
		p := numbered("/app/.git/objects/%02x/", 16+(i%48)) + numbered("obj%06x", i)
		es = append(es, File(p, spread(p, 200, 5*kib)))
	}
	for i := range 12 {
		p := numbered("/app/.git/logs/refs/heads/branch%02d", i)
		es = append(es, File(p, spread(p, 150, 2*kib)))
	}
	return es
}

// ---------------------------------------------------------------------------
// RUN npm install — identical content in both builds.
// ---------------------------------------------------------------------------

// npmInstallEntries is called once per image and must return the same specs
// both times: the *only* difference between the two npm layers is the layer
// mtime the caller supplies. Nothing here may depend on the image.
func npmInstallEntries() []EntrySpec {
	es := []EntrySpec{
		Dir("/app/node_modules"), Dir("/app/node_modules/.bin"),
		File("/app/node_modules/.package-lock.json", 183*kib),
	}
	packages := []struct {
		name string
		kib  int64
	}{
		{"express", 214}, {"body-parser", 58}, {"accepts", 12}, {"mime-types", 17},
		{"mime-db", 185}, {"qs", 62}, {"cookie", 9}, {"debug", 12}, {"ms", 4},
		{"on-finished", 7}, {"send", 24}, {"serve-static", 9}, {"etag", 5},
		{"fresh", 4}, {"ejs", 128}, {"lodash", 141}, {"fluent-ffmpeg", 148},
		{"async", 73}, {"ws", 132}, {"dotenv", 26}, {"pino", 214},
		{"sonic-boom", 21}, {"thread-stream", 34}, {"undici", 118}, {"sharp", 41},
		{"semver", 92}, {"nanoid", 8}, {"dayjs", 66},
	}
	for _, p := range packages {
		b := "/app/node_modules/" + p.name
		es = append(es,
			Dir(b), Dir(b+"/lib"),
			File(b+"/package.json", spread(b+"pkg", 700, 2600)),
			File(b+"/index.js", spread(b+"idx", 1500, 40*kib)),
			File(b+"/README.md", spread(b+"rd", 1200, 22*kib)),
			File(b+"/LICENSE", 1069),
		)
		n := 3
		if p.kib > 100 {
			n = 6
		}
		each := max(p.kib*kib/int64(n), 2*kib)
		for i := range n {
			es = append(es, File(numbered(b+"/lib/module%02d.js", i), each))
		}
	}
	es = append(es,
		Dir("/app/node_modules/sharp/build"), Dir("/app/node_modules/sharp/build/Release"),
		File("/app/node_modules/sharp/build/Release/sharp-linux-x64.node", 1200*kib, WithMode(0o755)),
		Dir("/app/node_modules/undici/lib/llhttp"),
		File("/app/node_modules/undici/lib/llhttp/llhttp.wasm", 262*kib),
		// The one file that makes node_modules dominate every size bar.
		File("/app/node_modules/lodash/lodash.js", 4*mib),
	)
	// npm's .bin entries are symlinks into the packages, not copies.
	for _, b := range []string{"ejs", "sharp", "semver", "nanoid", "undici", "pino"} {
		es = append(es, Symlink("/app/node_modules/.bin/"+b, "../"+b+"/index.js"))
	}
	// A deep path, so the tree view's indentation and column alignment are
	// exercised by the demo data itself (DESIGN §5.3, §11).
	babel := "/app/node_modules/@babel/plugin-transform-runtime"
	es = append(es,
		Dir("/app/node_modules/@babel"), Dir(babel), Dir(babel+"/lib"),
		Dir(babel+"/lib/get-runtime-path"),
		File(babel+"/package.json", 1522),
		File(babel+"/README.md", 2214),
		File(babel+"/LICENSE", 1106),
		File(babel+"/lib/index.js", 24618),
		File(babel+"/lib/definitions.js", 9412),
		File(babel+"/lib/helpers.js", 3187),
		File(babel+"/lib/get-runtime-path/index.js", 3121),
		File(babel+"/lib/get-runtime-path/browser.js", 918),
		Dir("/app/node_modules/@babel/runtime"),
		Dir("/app/node_modules/@babel/runtime/helpers"),
		Dir("/app/node_modules/@babel/runtime/helpers/esm"),
		File("/app/node_modules/@babel/runtime/package.json", 2380),
		File("/app/node_modules/@babel/runtime/helpers/inherits.js", 486),
		File("/app/node_modules/@babel/runtime/helpers/esm/inherits.js", 431),
	)
	return es
}

// ---------------------------------------------------------------------------
// RUN apt-get install ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/
// ---------------------------------------------------------------------------

// ffmpegWhiteoutTargets are the four directories the Dockerfile's cleanup
// removes. They existed in the base layer, so the layer's diff records them as
// explicit whiteouts — the deletions the filesystem view renders as removed
// subtrees.
var ffmpegWhiteoutTargets = []string{
	"/var/lib/apt",
	"/var/lib/dpkg",
	"/var/lib/cache",
	"/var/lib/log",
}

// ffmpegEntries is byte-identical between the two builds: the files come out
// of .deb archives with the archives' own timestamps, so a rebuild reproduces
// the layer exactly. The pair therefore gets one could-be-shared edge with
// DiffIDEqual == true, next to the npm edge's false — the two states the UI
// distinguishes, both present in the demo.
func ffmpegEntries() []EntrySpec {
	es := []EntrySpec{
		Dir("/etc/OpenCL"), Dir("/etc/OpenCL/vendors"), Dir("/usr/share/ffmpeg"),
		File("/usr/bin/ffmpeg", 279*kib, WithMode(0o755)),
		File("/usr/bin/ffprobe", 194*kib, WithMode(0o755)),
		File("/usr/bin/ffplay", 163*kib, WithMode(0o755)),
		File("/usr/share/ffmpeg/ffprobe.xsd", 12309),
		File("/etc/OpenCL/vendors/pocl.icd", 42),
	}
	for _, t := range []struct {
		name string
		size int64
	}{
		{"libavcodec.so.59.37.100", 1804 * kib}, {"libavformat.so.59.27.100", 484 * kib},
		{"libavutil.so.57.28.100", 134 * kib}, {"libavfilter.so.8.44.100", 989 * kib},
		{"libavdevice.so.59.7.100", 16 * kib}, {"libswscale.so.6.7.100", 76 * kib},
		{"libswresample.so.4.7.100", 17 * kib}, {"libpostproc.so.56.6.100", 13 * kib},
		{"libx264.so.164", 432 * kib}, {"libx265.so.199", 1426 * kib},
		{"libvpx.so.7.0", 417 * kib}, {"libaom.so.3.6", 620 * kib},
		{"libopus.so.0.8.0", 54 * kib}, {"libmp3lame.so.0", 53 * kib},
		{"libvorbis.so.0.4.9", 24 * kib}, {"libtheora.so.0.3.10", 40 * kib},
		{"libdav1d.so.6.6", 157 * kib}, {"libsvtav1enc.so.1", 896 * kib},
		{"libvidstab.so.1.1", 10 * kib}, {"libzimg.so.2.0", 104 * kib},
	} {
		es = append(es, File("/usr/lib/x86_64-linux-gnu/"+t.name, t.size))
	}
	for _, n := range []string{
		"ass", "bluray", "chromaprint", "codec2", "gme", "gsm", "mysofa", "openjp2",
		"rabbitmq", "rist", "rubberband", "shine", "snappy", "soxr", "speex",
		"srt-gnutls", "twolame", "vo-amrwbenc", "webpmux", "xvidcore", "zmq",
		"zvbi", "openmpt", "mfx",
	} {
		p := "/usr/lib/x86_64-linux-gnu/lib" + n + ".so"
		es = append(es, File(p, spread(p, 11*kib, 320*kib)))
	}
	for _, t := range ffmpegWhiteoutTargets {
		es = append(es, Whiteout(t))
	}
	return es
}
