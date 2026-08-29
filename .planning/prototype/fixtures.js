/* layerlens prototype fixtures — example:v1 vs example:v2 (see DESIGN.md §11)
 * Entry: {p:path, s:bytes} file add/overwrite; {p, d:true} directory; {p, del:true} whiteout (path + descendants).
 * Layers built with a seeded PRNG so sizes are stable across loads.
 */
(function () {
  let seed = 0x1ee7;
  const rnd = () => { seed |= 0; seed = (seed + 0x6D2B79F5) | 0; let t = Math.imul(seed ^ (seed >>> 15), 1 | seed); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
  const sz = (lo, hi) => Math.round(lo + rnd() * (hi - lo));
  const F = (p, s) => ({ p, s });

  // ---- trunk layer 1: debian bookworm base -------------------------------
  const base = [];
  ["bash:1234k", "ls:142k", "cat:43k", "cp:151k", "mv:147k", "rm:72k", "sh:125k", "grep:199k", "sed:110k", "tar:594k", "gzip:98k", "chmod:64k", "mkdir:88k", "dd:142k", "ln:74k"].forEach(x => {
    const [n, k] = x.split(":"); base.push(F("/bin/" + n, parseInt(k) * 1024));
  });
  ["awk", "find", "xargs", "sort", "head", "tail", "cut", "tr", "du", "df", "env", "id", "uname", "wc", "which"].forEach(n => base.push(F("/usr/bin/" + n, sz(30e3, 300e3))));
  base.push(F("/usr/lib/x86_64-linux-gnu/libc.so.6", 1922136));
  base.push(F("/usr/lib/x86_64-linux-gnu/libm.so.6", 907784));
  base.push(F("/usr/lib/x86_64-linux-gnu/libstdc++.so.6.0.30", 2260296));
  base.push(F("/usr/lib/x86_64-linux-gnu/libssl.so.3", 674704));
  base.push(F("/usr/lib/x86_64-linux-gnu/libcrypto.so.3", 4455368));
  base.push(F("/usr/lib/x86_64-linux-gnu/libz.so.1.2.13", 121280));
  for (let i = 0; i < 34; i++) base.push(F("/usr/lib/x86_64-linux-gnu/lib" + ["pcre2-8", "selinux", "acl", "attr", "blkid", "mount", "smartcols", "uuid", "tinfo", "gcrypt", "gpg-error", "lz4", "lzma", "zstd", "systemd-shared", "apt-pkg", "apt-private", "dpkg", "gmp", "hogweed", "nettle", "tasn1", "unistring", "idn2", "p11-kit", "ffi", "seccomp", "cap-ng", "audit", "pam", "crypt", "db-5.3", "bz2", "expat"][i] + ".so", sz(60e3, 1.6e6)));
  ["passwd:2.9k", "group:1.2k", "shadow:1.4k", "hostname:0.02k", "os-release:0.4k", "resolv.conf:0.1k", "fstab:0.7k", "profile:0.8k", "bash.bashrc:2.2k"].forEach(x => { const [n, k] = x.split(":"); base.push(F("/etc/" + n, Math.round(parseFloat(k) * 1024))); });
  base.push(F("/etc/apt/sources.list", 342));
  base.push(F("/var/lib/dpkg/status", 1184532));
  base.push(F("/var/lib/dpkg/available", 214056));
  for (let i = 0; i < 22; i++) base.push(F("/var/lib/dpkg/info/pkg" + i + ".list", sz(500, 24e3)));
  for (let i = 0; i < 18; i++) base.push(F("/usr/share/doc/pkg" + i + "/copyright", sz(1e3, 30e3)));
  base.push(F("/usr/share/zoneinfo/UTC", 118));
  for (let i = 0; i < 30; i++) base.push(F("/usr/share/zoneinfo/z" + i, sz(200, 3e3)));
  // pad to ~74.8 MiB with locale/terminfo data
  for (let i = 0; i < 26; i++) base.push(F("/usr/lib/locale/C.utf8/part" + i, sz(1.2e6, 3.4e6)));

  // ---- trunk layer 2: apt deps (curl, git, ca-certificates) --------------
  const deps = [F("/usr/bin/curl", 259632), F("/usr/bin/git", 3986648), F("/usr/bin/wget", 549384), F("/usr/bin/xz", 88408), F("/usr/bin/gpg", 1105324)];
  for (let i = 0; i < 12; i++) deps.push(F("/usr/lib/git-core/git-" + ["remote-https", "http-fetch", "daemon", "shell", "upload-pack", "receive-pack", "credential-store", "imap-send", "sh-i18n", "fast-import", "http-backend", "web--browse"][i], sz(0.9e6, 3.9e6)));
  for (let i = 0; i < 58; i++) deps.push(F("/etc/ssl/certs/cert-" + String(i).padStart(3, "0") + ".pem", sz(1200, 4200)));
  deps.push(F("/etc/ssl/certs/ca-certificates.crt", 219030));
  for (let i = 0; i < 8; i++) deps.push(F("/usr/lib/x86_64-linux-gnu/lib" + ["curl", "nghttp2", "ssh2", "psl", "brotlidec", "brotlicommon", "ldap", "sasl2"][i] + ".so", sz(120e3, 780e3)));
  for (let i = 0; i < 9; i++) deps.push(F("/usr/share/git-core/templates/hooks/h" + i + ".sample", sz(400, 4e3)));
  for (let i = 0; i < 6; i++) deps.push(F("/usr/share/gnupg/blob" + i, sz(2.4e6, 6.2e6)));

  // ---- trunk layer 3: node 24 runtime ------------------------------------
  const node = [F("/usr/local/bin/node", 119538904)];
  for (let i = 0; i < 40; i++) node.push(F("/usr/local/include/node/" + ["node.h", "node_api.h", "node_buffer.h", "node_version.h", "v8.h", "v8-array-buffer.h", "v8-context.h", "v8-data.h", "v8-date.h", "v8-debug.h", "v8-exception.h", "v8-external.h", "v8-function.h", "v8-internal.h", "v8-isolate.h", "v8-json.h", "v8-local-handle.h", "v8-locker.h", "v8-maybe.h", "v8-memory-span.h", "v8-message.h", "v8-microtask.h", "v8-object.h", "v8-persistent-handle.h", "v8-primitive.h", "v8-promise.h", "v8-proxy.h", "v8-regexp.h", "v8-script.h", "v8-snapshot.h", "v8-statistics.h", "v8-template.h", "v8-traced-handle.h", "v8-typed-array.h", "v8-unwinder.h", "v8-value.h", "v8-version.h", "v8-wasm.h", "uv.h", "zlib.h"][i], sz(3e3, 210e3)));
  const npmPkgs = ["semver", "glob", "minimatch", "which", "ini", "tar", "pacote", "cacache", "make-fetch-happen", "npm-package-arg", "libnpmexec", "libnpmpublish", "abbrev", "chalk", "ci-info", "cli-table3", "hosted-git-info", "json-parse-even-better-errors", "minipass", "node-gyp", "nopt", "normalize-package-data", "npm-audit-report", "proc-log", "read", "ssri", "treeverse", "validate-npm-package-name", "write-file-atomic", "graceful-fs"];
  npmPkgs.forEach(pk => { const b = "/usr/local/lib/node_modules/npm/node_modules/" + pk + "/"; node.push(F(b + "package.json", sz(600, 2500)), F(b + "lib/index.js", sz(2e3, 90e3)), F(b + "README.md", sz(1e3, 12e3))); });
  node.push(F("/usr/local/lib/node_modules/npm/bin/npm-cli.js", 54), F("/usr/local/lib/node_modules/npm/index.js", 4532), F("/usr/local/lib/node_modules/npm/package.json", 8843));
  for (let i = 0; i < 10; i++) node.push(F("/usr/local/lib/node_modules/npm/lib/commands/c" + i + ".js", sz(4e3, 40e3)));
  for (let i = 0; i < 12; i++) node.push(F("/usr/local/share/man/man1/node-" + i + ".1", sz(1e3, 9e3)));

  // ---- trunk layer 4: corepack/yarn --------------------------------------
  const yarn = [F("/opt/yarn-v1.22.22/bin/yarn.js", 5236831), F("/opt/yarn-v1.22.22/bin/yarn", 1225), F("/opt/yarn-v1.22.22/package.json", 2145), F("/usr/local/bin/yarn", 98), F("/usr/local/bin/yarnpkg", 98)];

  // ---- trunk layer 5: WORKDIR /app (empty-ish) ---------------------------
  const workdir = [{ p: "/app", d: true }];

  // ---- fork: COPY . . ----------------------------------------------------
  const appCommon = (mainSize, utilSize) => [
    F("/app/main.js", mainSize),
    F("/app/package.json", 1184),
    F("/app/package-lock.json", 186422),
    F("/app/src/index.js", 5240),
    F("/app/src/routes.js", 8931),
    F("/app/src/util.js", utilSize),
    F("/app/src/render.js", 3712),
    F("/app/views/home.ejs", 2210),
    F("/app/views/clip.ejs", 1874),
    F("/app/public/style.css", 6120),
    F("/app/public/app.js", 4488),
  ];
  const copyV1 = appCommon(4212, 2966).concat([F("/app/src/old-util.js", 1810)]); // v1 still ships old-util.js
  const copyV2 = appCommon(4907, 3105).concat([
    // the .dockerignore mistake: build context junk copied into the image
    F("/app/.env", 412),
    F("/app/debug.log", 1264880),
    F("/app/.git/HEAD", 23), F("/app/.git/config", 310), F("/app/.git/index", 20488),
    F("/app/.git/packed-refs", 1024), F("/app/.git/COMMIT_EDITMSG", 64),
    F("/app/.git/objects/pack/pack-8c4f21ab.pack", 37318932),
    F("/app/.git/objects/pack/pack-8c4f21ab.idx", 412876),
  ]);
  for (let i = 0; i < 40; i++) copyV2.push(F("/app/.git/objects/" + (10 + (i % 80)) + "/" + "obj" + i.toString(16).padStart(6, "0"), sz(200, 42e3)));
  for (let i = 0; i < 12; i++) copyV2.push(F("/app/.git/logs/refs/heads/branch" + i, sz(150, 2e3)));

  // ---- npm install (IDENTICAL content in both builds → could-be-shared) --
  const nm = [F("/app/node_modules/.package-lock.json", 187001)];
  const pkgs = [["express", 214], ["body-parser", 58], ["accepts", 12], ["mime-types", 17], ["mime-db", 185], ["qs", 62], ["cookie", 9], ["debug", 12], ["ms", 4], ["on-finished", 7], ["send", 24], ["serve-static", 9], ["etag", 5], ["fresh", 4], ["ejs", 128], ["lodash", 1410], ["fluent-ffmpeg", 148], ["async", 736], ["which", 7], ["ws", 132], ["dotenv", 26], ["pino", 214], ["fast-json-stringify", 88], ["sonic-boom", 21], ["thread-stream", 34], ["undici", 1180], ["sharp", 412], ["semver", 92], ["nanoid", 8], ["dayjs", 668]];
  pkgs.forEach(([pk, kb]) => {
    const b = "/app/node_modules/" + pk + "/";
    const total = kb * 1024; const main = Math.round(total * 0.55);
    nm.push(F(b + "package.json", sz(700, 2600)), F(b + "index.js", Math.min(main, sz(1500, 60e3))), F(b + "README.md", sz(1200, 22e3)), F(b + "LICENSE", 1069));
    let rest = Math.max(total - 30e3, 8e3); const n = 3 + (kb > 100 ? 5 : 2);
    for (let i = 0; i < n; i++) nm.push(F(b + "lib/" + pk.replace(/[^a-z]/g, "") + "-" + i + ".js", Math.round(rest / n)));
  });
  nm.push(F("/app/node_modules/sharp/build/Release/sharp-linux-x64.node", 9834552));
  nm.push(F("/app/node_modules/undici/lib/llhttp/llhttp.wasm", 2148069));
  for (let i = 0; i < 14; i++) nm.push(F("/app/node_modules/.bin/bin" + i, sz(300, 2e3)));
  nm.push(F("/app/node_modules/lodash/lodash.js", 66601536)); // fat demo blob so node_modules dominates
  // deep-indent case (DESIGN.md §5.3): proves header/row column alignment holds at depth
  const babel = "/app/node_modules/@babel/plugin-transform-runtime/";
  nm.push(F(babel + "package.json", 1522), F(babel + "README.md", 2214), F(babel + "LICENSE", 1106));
  nm.push(F(babel + "lib/index.js", 24618), F(babel + "lib/definitions.js", 9412), F(babel + "lib/helpers.js", 3187));
  nm.push(F(babel + "lib/get-runtime-path/index.js", 3121), F(babel + "lib/get-runtime-path/browser.js", 918));
  nm.push(F("/app/node_modules/@babel/runtime/package.json", 2380), F("/app/node_modules/@babel/runtime/helpers/esm/inherits.js", 431), F("/app/node_modules/@babel/runtime/helpers/inherits.js", 486));

  // ---- ffmpeg apt layer (IDENTICAL in both → could-be-shared) ------------
  const ff = [F("/usr/bin/ffmpeg", 285816), F("/usr/bin/ffprobe", 199224), F("/usr/bin/ffplay", 166840)];
  const avlibs = [["libavcodec.so.59.37.100", 14783608], ["libavformat.so.59.27.100", 3969800], ["libavutil.so.57.28.100", 1096824], ["libavfilter.so.8.44.100", 8103696], ["libavdevice.so.59.7.100", 128248], ["libswscale.so.6.7.100", 619776], ["libswresample.so.4.7.100", 133432], ["libpostproc.so.56.6.100", 100664], ["libx264.so.164", 3538648], ["libx265.so.199", 11681472], ["libvpx.so.7.0", 3417352], ["libaom.so.3.6", 5083208], ["libopus.so.0.8.0", 439496], ["libmp3lame.so.0", 429624], ["libvorbis.so.0.4.9", 195272], ["libtheora.so.0.3.10", 322920], ["libdav1d.so.6.6", 1288008], ["libsvtav1enc.so.1", 7345224], ["libvidstab.so.1.1", 76136], ["libzimg.so.2.0", 852288]];
  avlibs.forEach(([n, s]) => ff.push(F("/usr/lib/x86_64-linux-gnu/" + n, s)));
  for (let i = 0; i < 24; i++) ff.push(F("/usr/lib/x86_64-linux-gnu/lib" + ["ass", "bluray", "chromaprint", "codec2", "gme", "gsm", "mysofa", "openjp2", "rabbitmq", "rist", "rubberband", "shine", "snappy", "soxr", "speex", "srt-gnutls", "twolame", "vo-amrwbenc", "webpmux", "xvidcore", "zmq", "zvbi", "openmpt", "mfx"][i] + ".so", sz(90e3, 2.6e6)));
  ff.push(F("/usr/share/ffmpeg/ffprobe.xsd", 12309), F("/etc/OpenCL/vendors/pocl.icd", 42));
  ff.push({ p: "/var/lib/apt", del: true }, { p: "/var/lib/dpkg", del: true }, { p: "/var/cache", del: true }, { p: "/var/log", del: true });

  const sum = es => es.reduce((a, e) => a + (e.s || 0), 0);
  const short = h => h.slice(0, 4) + "…" + h.slice(-4);
  const L = (id, hex, kw, txt, raw, entries, extra) => Object.assign({
    id, kw, txt, raw, entries, size: sum(entries),
    diffid: "sha256:" + hex, digestShort: short(hex),
  }, extra || {});

  window.FIXTURES = {
    imageA: { ref: "example:v1", digest: "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", size: 0, built: "2026-08-27 14:02" },
    imageB: { ref: "example:v2", digest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", size: 0, built: "2026-08-29 09:41" },
    trunk: [
      L("t1", "a1b9e4d02c6f8a17530b9cd42e1f7a6688d90c3b12ef45a7b8c9d0e1f2a3b4c5", "ADD", "file:4d192565a7220e13… in /", "ADD file:4d192565a7220e135cab8c6c9d3e6f2f8e4b5a6c7d8e9f0a1b2c3d4e5f6a7b8 in /", base),
      L("t2", "b2c8f5e13d7a9b2864a1de53f2a8b7799ea1d4c23fa056b8c9dae1f2a3b4c5d6", "RUN", "set -eux; apt-get update; apt-get install -y ca-certificates curl git wget gnupg dirmngr xz-utils …", "RUN /bin/sh -c set -eux; apt-get update; apt-get install -y --no-install-recommends ca-certificates curl git wget gnupg dirmngr xz-utils libatomic1; rm -rf /var/lib/apt/lists/* # buildkit", deps),
      L("t3", "c3d9a6f24e8bac3975b2ef64a3b9c88aafb2e5d34ab167c9daebf2a3b4c5d6e7", "RUN", "ARCH= && dpkgArch=… && curl -fsSLO https://nodejs.org/dist/v24.4.0/node-v24…", "RUN /bin/sh -c ARCH= && dpkgArch=\"$(dpkg --print-architecture)\" && curl -fsSLO --compressed \"https://nodejs.org/dist/v24.4.0/node-v24.4.0-linux-x64.tar.xz\" && tar -xJf … -C /usr/local --strip-components=1 # buildkit", node),
      L("t4", "d4eab7a35f9bd4a86c3fa075b4cad99bbac3f6e45bc278dae0fca3b4c5d6e7f8", "RUN", "set -ex && for key in 6A010C51…; do gpg …; done && curl -fsSLO yarn-v1.22.22…", "RUN /bin/sh -c set -ex && curl -fsSLO --compressed \"https://yarnpkg.com/downloads/1.22.22/yarn-v1.22.22.tar.gz\" && mkdir -p /opt && tar -xzf yarn-v1.22.22.tar.gz -C /opt/ # buildkit", yarn),
      L("t5", "e5fbc8b46aace5b97d4ab186c5dbeaaccbd4a7f56cd389ebf1adb4c5d6e7f8a9", "WORKDIR", "/app", "WORKDIR /app", workdir),
    ],
    branchA: [
      L("a6", "f6acd9c57bbdf6ca8e5bc297d6ecfbbddce5b8a67de49afc02bec5d6e7f8a9b0", "COPY", ". .", "COPY . . # buildkit", copyV1),
      L("a7", "a7bdeada68ccf7db9f6cd3a8e7fdacceddf6c9b78ef5abad13cfd6e7f8a9b0c1", "RUN", "npm install", "RUN /bin/sh -c npm install # buildkit", nm, { shareKey: "npm" }),
      L("a8", "b8cefbeb79ddf8ec0a7de4b9f8aebddfeea7dac89fa6bcbe24dae7f8a9b0c1d2", "RUN", "apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/", "RUN /bin/sh -c apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/ # buildkit", ff, { shareKey: "ffmpeg" }),
    ],
    branchB: [
      L("b6", "c9dfacfc8aeef9fd1b8ef5caa9bfceeaffb8ebd9aab7cdcf35ebf8a9b0c1d2e3", "COPY", ". .", "COPY . . # buildkit", copyV2),
      L("b7", "daeabdad9bffaafe2c9fa6dbbacadffbaac9fceabbc8deda46fca9b0c1d2e3f4", "RUN", "npm install", "RUN /bin/sh -c npm install # buildkit", nm, { shareKey: "npm" }),
      L("b8", "ebfbcebeacaabbaf3daab7eccbdbeaacbbdaadfbccd9efeb57adb0c1d2e3f4a5", "RUN", "apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/", "RUN /bin/sh -c apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/ # buildkit", ff, { shareKey: "ffmpeg" }),
    ],
    // image selection view listings
    analyzed: [
      { ref: "example:v1", digest: "9f86…0a08", size: "459 MiB", layers: 8, when: "analyzed 2 h ago", demo: true },
      { ref: "example:v2", digest: "e3b0…b855", size: "497 MiB", layers: 8, when: "analyzed 2 h ago", demo: true },
      { ref: "nginx:1.27", digest: "6c8a…41d2", size: "192 MiB", layers: 7, when: "analyzed yesterday" },
      { ref: "python:3.12-slim", digest: "b2f1…9ac3", size: "125 MiB", layers: 5, when: "analyzed 3 d ago" },
    ],
    daemon: [
      { ref: "myapp:latest", digest: "77aa…d901", size: "1.24 GiB", layers: 14, when: "linux/amd64 · not analyzed" },
      { ref: "postgres:16.4", digest: "18cf…22be", size: "438 MiB", layers: 13, when: "linux/amd64 · not analyzed" },
      { ref: "redis:7.4-alpine", digest: "c05d…8e77", size: "41.2 MiB", layers: 8, when: "linux/amd64 · analyzed" },
    ],
  };
  const FX = window.FIXTURES;
  FX.imageA.size = FX.trunk.reduce((a, l) => a + l.size, 0) + FX.branchA.reduce((a, l) => a + l.size, 0);
  FX.imageB.size = FX.trunk.reduce((a, l) => a + l.size, 0) + FX.branchB.reduce((a, l) => a + l.size, 0);
})();
