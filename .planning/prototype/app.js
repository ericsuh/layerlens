/* layerlens prototype — see .planning/DESIGN.md */
(function () {
  const FX = window.FIXTURES;
  const $ = (s, el) => (el || document).querySelector(s);
  const el = (tag, cls, html) => { const e = document.createElement(tag); if (cls) e.className = cls; if (html != null) e.innerHTML = html; return e; };
  const esc = s => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

  // ---------- formatting ----------
  function human(n) {
    if (n === 0) return "0 B";
    const u = ["B", "KiB", "MiB", "GiB", "TiB"]; let i = 0; let v = n;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    const s = i === 0 ? String(v) : v >= 100 ? Math.round(v).toString() : v.toFixed(1);
    return s + " " + u[i];
  }
  const signed = n => n === 0 ? "—" : (n > 0 ? "+" : "−") + human(Math.abs(n));
  const signedN = n => n === 0 ? "—" : (n > 0 ? "+" : "−") + Math.abs(n).toLocaleString();

  // ---------- state ----------
  const trunkLen = FX.trunk.length;
  const stackA = FX.trunk.concat(FX.branchA);
  const stackB = FX.trunk.concat(FX.branchB);
  const state = {
    view: "select", tab: "analyzed",
    slotA: FX.analyzed[0], slotB: FX.analyzed[1], armed: "a",
    selA: stackA.length - 1, selB: stackB.length - 1,
    root: "/", expanded: new Set(["/app"]), filter: "changed", search: "",
    regText: "", pulling: false,
  };

  // ---------- theme ----------
  const themeBtn = $("#themeBtn");
  function setTheme(t) { document.documentElement.dataset.theme = t; themeBtn.textContent = t === "dark" ? "☀" : "☾"; themeBtn.title = "Switch to " + (t === "dark" ? "light" : "dark") + " theme"; }
  themeBtn.addEventListener("click", () => setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark"));
  setTheme("light");

  // ---------- cumulative filesystem + diff ----------
  function cumulative(stack, upto) {
    const files = new Map(); const dirs = new Set();
    for (let i = 0; i <= upto; i++) for (const e of stack[i].entries) {
      if (e.del) {
        for (const k of [...files.keys()]) if (k === e.p || k.startsWith(e.p + "/")) files.delete(k);
        for (const d of [...dirs]) if (d === e.p || d.startsWith(e.p + "/")) dirs.delete(d);
      } else if (e.d) dirs.add(e.p);
      else files.set(e.p, e.s);
    }
    return { files, dirs };
  }
  function mkNode(name, kind) {
    return { name, kind, children: new Map(), aSize: 0, bSize: 0, aFiles: 0, bFiles: 0, add: 0, rem: 0, modB: 0, uBytes: 0, addN: 0, remN: 0, modN: 0, status: "unchanged", presentA: false, presentB: false };
  }
  function buildTree(fsA, fsB) {
    const root = mkNode("", "dir"); root.presentA = root.presentB = true;
    function descend(parts, kind) {
      let node = root; const chain = [root];
      for (let i = 0; i < parts.length; i++) {
        let c = node.children.get(parts[i]);
        if (!c) { c = mkNode(parts[i], i === parts.length - 1 ? kind : "dir"); node.children.set(parts[i], c); }
        node = c; chain.push(node);
      }
      return chain;
    }
    const all = new Set([...fsA.files.keys(), ...fsB.files.keys()]);
    for (const p of all) {
      const a = fsA.files.has(p) ? fsA.files.get(p) : null;
      const b = fsB.files.has(p) ? fsB.files.get(p) : null;
      const chain = descend(p.split("/").filter(Boolean), "file");
      const leaf = chain[chain.length - 1];
      const status = a == null ? "added" : b == null ? "removed" : a === b ? "unchanged" : "modified";
      leaf.status = status; leaf.aOwn = a; leaf.bOwn = b;
      for (const n of chain) {
        if (a != null) { n.aSize += a; n.aFiles++; n.presentA = true; }
        if (b != null) { n.bSize += b; n.bFiles++; n.presentB = true; }
        if (status === "added") { n.add += b; n.addN++; }
        else if (status === "removed") { n.rem += a; n.remN++; }
        else if (status === "modified") { n.modB += Math.max(a, b); n.modN++; }
        else n.uBytes += a;
      }
    }
    for (const d of new Set([...fsA.dirs, ...fsB.dirs])) {
      const chain = descend(d.split("/").filter(Boolean), "dir");
      const leaf = chain[chain.length - 1];
      if (fsA.dirs.has(d)) leaf.presentA = true;
      if (fsB.dirs.has(d)) leaf.presentB = true;
    }
    (function finalize(n) {
      for (const c of n.children.values()) finalize(c);
      if (n.kind === "dir" && n !== root) {
        if (!n.presentA && n.presentB) n.status = "added";
        else if (n.presentA && !n.presentB) n.status = "removed";
        else if (n.addN + n.remN + n.modN > 0) n.status = "contains";
        else n.status = "unchanged";
      }
    })(root);
    return root;
  }
  let diffRoot = null, diffStats = null;
  function recomputeDiff() {
    const fsA = cumulative(stackA, state.selA), fsB = cumulative(stackB, state.selB);
    diffRoot = buildTree(fsA, fsB);
    diffStats = { total: diffRoot.aFiles + diffRoot.addN, changed: diffRoot.addN + diffRoot.remN + diffRoot.modN };
    if (!nodeAt(state.root)) { state.root = "/"; }
  }
  function nodeAt(path) {
    if (path === "/") return diffRoot;
    let n = diffRoot;
    for (const part of path.split("/").filter(Boolean)) { n = n && n.children.get(part); }
    return n;
  }

  // ---------- selection view ----------
  const refShort = r => r.length > 34 ? "…" + r.slice(-33) : r;
  function slotHtml(which) {
    const img = which === "a" ? state.slotA : state.slotB;
    const slot = $("#slot" + which.toUpperCase());
    slot.className = "slot " + (img ? "filled " + which : "empty") + (state.armed === which ? " armed" : "");
    slot.setAttribute("aria-label", "Image " + which.toUpperCase() + " slot" + (state.armed === which ? " (active)" : ""));
    if (img) {
      slot.innerHTML = '<span class="badge ' + which + '">' + which.toUpperCase() + "</span>" +
        '<span class="slot-main"><span class="slot-ref" title="' + esc(img.ref) + '">' + esc(img.ref) + "</span>" +
        '<span class="slot-meta">' + img.size + " · " + img.layers + ' layers · <span class="mono">sha256:' + img.digest + "</span></span></span>" +
        '<button class="slot-x" title="Remove from slot ' + which.toUpperCase() + '" aria-label="Remove">✕</button>';
      $(".slot-x", slot).addEventListener("click", ev => { ev.stopPropagation(); setSlot(which, null); });
    } else {
      slot.innerHTML = '<span class="badge ' + which + '">' + which.toUpperCase() + "</span>" +
        '<span class="slot-main"><span class="slot-hint">Select an image below</span></span>';
    }
  }
  function setSlot(which, img) {
    if (which === "a") state.slotA = img; else state.slotB = img;
    if (img) state.armed = which === "a" ? (state.slotB ? "a" : "b") : (state.slotA ? "b" : "a");
    else state.armed = which;
    renderSelect();
  }
  function pickImage(img, which) {
    const w = which || state.armed;
    if (state.slotA && state.slotA.ref === img.ref && w !== "a") { /* fallthrough */ }
    if ((state.slotA === img && w === "a") || (state.slotB === img && w === "b")) return setSlot(w, null);
    setSlot(w, img);
  }
  function rowHtml(img, list) {
    const inA = state.slotA && state.slotA.ref === img.ref, inB = state.slotB && state.slotB.ref === img.ref;
    const row = el("button", "src-row" + (inA ? " in-a" : "") + (inB ? " in-b" : ""));
    row.innerHTML =
      (inA ? '<span class="badge a row-badge">A</span>' : inB ? '<span class="badge b row-badge">B</span>' : "") +
      '<span class="src-ref" title="' + esc(img.ref) + '">' + esc(img.ref) + "</span>" +
      (img.demo ? '<span class="chip demo">demo</span>' : "") +
      '<span class="src-digest">' + img.digest + "</span>" +
      '<span class="src-meta">' + img.size + " · " + img.layers + " layers<br>" + img.when + "</span>" +
      '<span class="set-btns"><button class="set-btn a">Set A</button><button class="set-btn b">Set B</button></span>';
    row.addEventListener("click", () => pickImage(img));
    $(".set-btn.a", row).addEventListener("click", ev => { ev.stopPropagation(); pickImage(img, "a"); });
    $(".set-btn.b", row).addEventListener("click", ev => { ev.stopPropagation(); pickImage(img, "b"); });
    list.appendChild(row);
  }
  const ALLOW = [/^(index\.)?docker\.io$/, /^ghcr\.io$/, /^gcr\.io$/, /^public\.ecr\.aws$/, /^[a-z0-9-]+\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com$/, /^[a-z0-9-]+\.azurecr\.io$/];
  function parseRegistry(ref) {
    if (!/^[a-z0-9]/i.test(ref)) return null;
    const first = ref.split("/")[0];
    const hasHost = ref.includes("/") && (first.includes(".") || first.includes(":"));
    return hasHost ? first.split(":")[0] : "docker.io";
  }
  function renderRegVerdict() {
    const v = $("#regVerdict"), input = $("#regInput"), btn = $("#regFetch");
    const t = state.regText.trim();
    input.classList.remove("bad");
    if (!t) { v.textContent = ""; v.className = "reg-verdict"; btn.disabled = true; return; }
    const reg = parseRegistry(t);
    if (!reg || /\s/.test(t)) { v.textContent = "Not a valid image reference"; v.className = "reg-verdict bad"; input.classList.add("bad"); btn.disabled = true; return; }
    const ok = ALLOW.some(rx => rx.test(reg));
    v.innerHTML = ok ? "→ " + esc(reg) + " ✓ allowed" : "→ " + esc(reg) + " — not on the allowlist (Docker Hub, GHCR, GCR, ECR, ACR)";
    v.className = "reg-verdict " + (ok ? "ok" : "bad"); if (!ok) input.classList.add("bad");
    btn.disabled = !ok;
  }
  function renderSelect() {
    slotHtml("a"); slotHtml("b");
    const both = state.slotA && state.slotB;
    $("#compareBtn").disabled = !both;
    $("#compareHint").textContent = both
      ? (state.slotA.ref === state.slotB.ref ? "Both slots contain the same image — every layer will be shared." : "")
      : "Choose two images to compare";
    document.querySelectorAll(".tab").forEach(t => t.setAttribute("aria-selected", t.dataset.tab === state.tab));
    document.querySelectorAll(".tabpanel").forEach(p => { p.hidden = p.dataset.tab !== state.tab; });
    const la = $("#listAnalyzed"); la.innerHTML = ""; FX.analyzed.forEach(i => rowHtml(i, la));
    const ld = $("#listDaemon"); ld.innerHTML = ""; FX.daemon.forEach(i => rowHtml(i, ld));
    $("#pullCard").hidden = !state.pulling;
    renderRegVerdict();
  }
  document.querySelectorAll(".tab").forEach(t => t.addEventListener("click", () => { state.tab = t.dataset.tab; renderSelect(); }));
  $("#slotA").addEventListener("click", () => { state.armed = "a"; renderSelect(); });
  $("#slotB").addEventListener("click", () => { state.armed = "b"; renderSelect(); });
  $("#regInput").addEventListener("input", ev => { state.regText = ev.target.value; renderRegVerdict(); });
  $("#regFetch").addEventListener("click", () => { state.pulling = true; renderSelect(); });
  $("#pullCancel").addEventListener("click", () => { state.pulling = false; renderSelect(); });
  $("#compareBtn").addEventListener("click", () => { state.view = "browse"; render(); });
  $("#backBtn").addEventListener("click", () => { state.view = "select"; render(); });

  // ---------- layer diagram ----------
  function layerCard(layer, cls, sideIdx, maxSize) {
    const card = el("button", "lcard " + cls);
    card.setAttribute("role", "radio");
    const pct = layer.size === 0 ? 0 : Math.max(2, Math.round(layer.size / maxSize * 100));
    const shareChip = layer.shareKey ? '<span class="approx-chip" title="Identical content (files + permissions, timestamps ignored) to a layer in the other image — could have shared the cache, but a differing layer above broke it.">≈</span>' : "";
    const sharedTag = cls.indexOf("shared") >= 0 ? '<span class="shared-tag">SHARED</span>' : "";
    card.innerHTML =
      '<span class="radio" aria-hidden="true"></span>' +
      '<span class="lc-main">' +
      '<span class="lc-ins"><span class="lc-kw">' + layer.kw + '</span><span class="lc-txt" title="' + esc(layer.raw) + '">' + esc(layer.txt) + "</span></span>" +
      '<span class="lc-meta"><span class="lc-size num">' + human(layer.size) + (layer.size === 0 ? " · empty" : "") + "</span>" +
      '<span class="lc-bar' + (layer.size === 0 ? " none" : "") + '"><i style="width:' + pct + '%"></i></span>' +
      '<span class="lc-digest" title="DiffID ' + layer.diffid + '">' + layer.digestShort + "</span></span>" +
      "</span>" + shareChip + sharedTag;
    return card;
  }
  function renderDiagram() {
    const maxSize = Math.max(...stackA.map(l => l.size), ...stackB.map(l => l.size));
    const trunkEl = $("#trunk"); trunkEl.innerHTML = "";
    FX.trunk.forEach((l, i) => {
      const sel = state.selA === i && state.selB === i;
      const c = layerCard(l, "shared" + (sel ? " sel" : ""), i, maxSize);
      c.setAttribute("aria-checked", sel);
      c.title = "Shared layer — selecting sets both comparison points";
      c.addEventListener("click", () => { state.selA = i; state.selB = i; render(); });
      trunkEl.appendChild(c);
    });
    const bA = $("#branchA"), bB = $("#branchB"); bA.innerHTML = ""; bB.innerHTML = "";
    FX.branchA.forEach((l, i) => {
      const idx = trunkLen + i; const sel = state.selA === idx;
      const c = layerCard(l, "ia" + (sel ? " sel" : ""), idx, maxSize);
      c.setAttribute("aria-checked", sel);
      c.addEventListener("click", () => { state.selA = idx; render(); });
      c.addEventListener("mouseenter", () => hl(l.shareKey, true)); c.addEventListener("mouseleave", () => hl(l.shareKey, false));
      c.dataset.share = l.shareKey || ""; bA.appendChild(c);
    });
    FX.branchB.forEach((l, i) => {
      const idx = trunkLen + i; const sel = state.selB === idx;
      const c = layerCard(l, "ib" + (sel ? " sel" : ""), idx, maxSize);
      c.setAttribute("aria-checked", sel);
      c.addEventListener("click", () => { state.selB = idx; render(); });
      c.addEventListener("mouseenter", () => hl(l.shareKey, true)); c.addEventListener("mouseleave", () => hl(l.shareKey, false));
      c.dataset.share = l.shareKey || ""; bB.appendChild(c);
    });
    $("#selChipA").textContent = "A @ layer " + (state.selA + 1);
    $("#selChipB").textContent = "B @ layer " + (state.selB + 1);
    requestAnimationFrame(drawEdges);
  }
  function hl(key, on) {
    if (!key) return;
    document.querySelectorAll('.lcard[data-share="' + key + '"]').forEach(c => c.classList.toggle("hl", on));
    document.querySelectorAll('.edge-dot[data-share="' + key + '"]').forEach(p => p.setAttribute("stroke-width", on ? 3 : 2));
  }
  function drawEdges() {
    const dia = $("#diagram"); const svg = $("#edges");
    const dr = dia.getBoundingClientRect();
    svg.setAttribute("viewBox", "0 0 " + dr.width + " " + dia.scrollHeight);
    svg.innerHTML = "";
    document.querySelectorAll(".edge-pill,.sel-rule").forEach(e => e.remove());
    const rel = r => ({ x: r.left - dr.left, y: r.top - dr.top, w: r.width, h: r.height });
    const trunkCards = [...$("#trunk").children].map(c => rel(c.getBoundingClientRect()));
    const aCards = [...$("#branchA").children].map(c => rel(c.getBoundingClientRect()));
    const bCards = [...$("#branchB").children].map(c => rel(c.getBoundingClientRect()));
    const P = (d, cls, dash) => {
      const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
      p.setAttribute("d", d); p.setAttribute("fill", "none"); p.setAttribute("stroke-width", "2");
      p.setAttribute("class", cls); if (dash) { p.setAttribute("stroke-dasharray", "2 5"); p.setAttribute("stroke-linecap", "round"); }
      svg.appendChild(p); return p;
    };
    const css = n => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
    if (trunkCards.length) {
      const t0 = trunkCards[0], tn = trunkCards[trunkCards.length - 1];
      const cx = t0.x + t0.w / 2;
      P("M" + cx + " " + (t0.y + t0.h) + " V" + tn.y, "").setAttribute("stroke", css("--border-strong"));
      // fork elbows
      if (aCards.length) {
        const a0 = aCards[0]; const ax = a0.x + a0.w / 2;
        const p = P("M" + cx + " " + (tn.y + tn.h) + " C " + cx + " " + (tn.y + tn.h + 22) + ", " + ax + " " + (a0.y - 22) + ", " + ax + " " + a0.y, "");
        p.setAttribute("stroke", css("--image-a"));
      }
      if (bCards.length) {
        const b0 = bCards[0]; const bx = b0.x + b0.w / 2;
        const p = P("M" + cx + " " + (tn.y + tn.h) + " C " + cx + " " + (tn.y + tn.h + 22) + ", " + bx + " " + (b0.y - 22) + ", " + bx + " " + b0.y, "");
        p.setAttribute("stroke", css("--image-b"));
      }
    }
    const spine = (cards, color) => {
      for (let i = 0; i + 1 < cards.length; i++) {
        const cx = cards[i].x + cards[i].w / 2;
        const p = P("M" + cx + " " + (cards[i].y + cards[i].h) + " V" + cards[i + 1].y, "");
        p.setAttribute("stroke", color); p.setAttribute("opacity", ".55");
      }
    };
    spine(aCards, css("--image-a")); spine(bCards, css("--image-b"));
    // could-be-shared dotted edges
    FX.branchA.forEach((l, i) => {
      if (!l.shareKey) return;
      const j = FX.branchB.findIndex(x => x.shareKey === l.shareKey);
      if (j < 0) return;
      const a = aCards[i], b = bCards[j];
      const y1 = a.y + a.h / 2, y2 = b.y + b.h / 2;
      const midY = Math.min(a.y, b.y) - 14;
      const p = P("M" + (a.x + a.w) + " " + y1 + " C " + (a.x + a.w + 22) + " " + midY + ", " + (b.x - 22) + " " + midY + ", " + b.x + " " + y2, "edge-dot", true);
      p.setAttribute("stroke", css("--shared")); p.dataset.share = l.shareKey;
      const pill = el("button", "edge-pill", "≈ same content");
      pill.title = "These layers contain identical files (content + permissions; timestamps ignored). The differing COPY layer above them broke the shared cache — they could have been shared.";
      pill.style.left = (a.x + a.w + (b.x - a.x - a.w) / 2) + "px"; pill.style.top = midY + "px";
      pill.addEventListener("mouseenter", () => hl(l.shareKey, true)); pill.addEventListener("mouseleave", () => hl(l.shareKey, false));
      dia.appendChild(pill);
    });
    // selection rules
    const rule = (side, card, halfLeft) => {
      const r = el("div", "sel-rule " + side);
      r.style.top = (card.y + card.h + 6) + "px";
      r.style.left = (halfLeft == null ? card.x : halfLeft) + "px";
      r.style.width = card.w + "px";
      const lbl = el("span", "lbl", (side === "a" ? "A" : "B") + " @ layer " + ((side === "a" ? state.selA : state.selB) + 1));
      if (side === "a" && state.selA === state.selB && state.selA < trunkLen) lbl.style.right = "auto", lbl.style.left = "0";
      r.appendChild(lbl); dia.appendChild(r);
    };
    if (state.selA < trunkLen) rule("a", trunkCards[state.selA]); else rule("a", aCards[state.selA - trunkLen]);
    if (state.selB < trunkLen) rule("b", trunkCards[state.selB]); else rule("b", bCards[state.selB - trunkLen]);
  }

  // ---------- filesystem tree ----------
  function sortedChildren(node) {
    return [...node.children.values()].sort((a, b) => {
      if ((a.kind === "dir") !== (b.kind === "dir")) return a.kind === "dir" ? -1 : 1;
      return Math.max(b.aSize, b.bSize) - Math.max(a.aSize, a.bSize) || a.name.localeCompare(b.name);
    });
  }
  function nodeTotal(n) { return n.uBytes + n.add + n.rem + n.modB; }
  function matchesSearch(n, q) {
    if (n.name.toLowerCase().includes(q)) return true;
    for (const c of n.children.values()) if (matchesSearch(c, q)) return true;
    return false;
  }
  function renderCrumbs() {
    const cr = $("#crumbs"); cr.innerHTML = "";
    const parts = state.root.split("/").filter(Boolean);
    const mk = (label, path, current) => {
      const b = el("button", "crumb", esc(label));
      if (current) b.setAttribute("aria-current", "page"); else b.addEventListener("click", () => { state.root = path; renderTree(); });
      cr.appendChild(b);
    };
    mk("/", "/", parts.length === 0);
    let acc = "";
    const collapse = parts.length > 4;
    parts.forEach((p, i) => {
      acc += "/" + p;
      if (collapse && i > 0 && i < parts.length - 2) {
        if (i === 1) { cr.appendChild(el("span", "crumb-sep", "›")); cr.appendChild(el("span", "crumb", "…")); }
        return;
      }
      cr.appendChild(el("span", "crumb-sep", "›"));
      mk(p, acc, i === parts.length - 1);
    });
  }
  const compact = n => n < 1000 ? String(n) : new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(n);
  function rowFor(node, path, depth) {
    const row = el("div", "trow tgrid st-" + node.status);
    row.setAttribute("role", "treeitem"); row.setAttribute("aria-level", depth + 1);
    const isDir = node.kind === "dir";
    const open = state.expanded.has(path) || !!state.search;
    if (isDir) row.setAttribute("aria-expanded", open);
    const name = el("div", "cell-name");
    const guides = el("span", "tguides");
    for (let i = 0; i < depth; i++) guides.appendChild(el("span", "tguide"));
    name.appendChild(guides);
    const chev = el("button", "chev" + (open ? " open" : "") + (isDir ? "" : " leaf"),
      '<svg width="10" height="10" viewBox="0 0 10 10"><path d="M3 1l4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>');
    chev.title = open ? "Collapse" : "Expand";
    name.appendChild(chev);
    const fn = el("button", "fname " + (isDir ? "dir" : "file"), esc(node.name) + (isDir ? "/" : ""));
    fn.title = path + " — " + srText(node);
    name.appendChild(fn);
    if (isDir) {
      const drill = el("button", "drill dir", "↳"); drill.title = "Open " + path + " as root";
      drill.addEventListener("click", ev => { ev.stopPropagation(); state.root = path; state.expanded.add(path); renderTree(); });
      name.appendChild(drill);
      const toggle = () => { open ? state.expanded.delete(path) : state.expanded.add(path); renderTree(); };
      chev.addEventListener("click", toggle); fn.addEventListener("click", toggle);
    }
    row.appendChild(name);
    // status
    let st;
    const nCh = node.addN + node.remN + node.modN;
    if (node.status === "added") st = "+"; else if (node.status === "removed") st = "−";
    else if (node.status === "modified") st = "±"; else if (node.status === "contains") st = '<span class="contains" title="' + nCh.toLocaleString() + ' changed descendants">± ' + compact(nCh) + "</span>";
    else st = "·";
    row.appendChild(el("div", "cell-status", st));
    // Δ size — signed human size; headers carry the meaning, cells carry only the value
    const dBytes = node.bSize - node.aSize;
    const dcls = node.status === "modified" || node.status === "contains" ? (dBytes > 0 ? "pos" : dBytes < 0 ? "neg" : "zero") : node.status === "added" ? "pos" : node.status === "removed" ? "neg" : "zero";
    row.appendChild(el("div", "cell-delta num " + dcls, signed(dBytes)));
    // Δ files — signed count, no unit suffix (the header labels it); blank on file rows
    const dFiles = node.bFiles - node.aFiles;
    row.appendChild(el("div", "cell-dfiles num " + (node.kind === "file" ? "" : dFiles > 0 ? "pos" : dFiles < 0 ? "neg" : "zero"), node.kind === "file" ? "" : dFiles === 0 ? "—" : signedN(dFiles)));
    // Size + Files — B-side absolutes (A-side struck through when removed; A totals in tooltip)
    const gone = node.status === "removed";
    const abTitle = "A: " + human(node.aSize) + " (" + node.aFiles.toLocaleString() + " files) → B: " + human(node.bSize) + " (" + node.bFiles.toLocaleString() + " files)";
    const sizeCell = el("div", "cell-size num" + (gone ? " gone" : ""), human(gone ? node.aSize : node.bSize));
    sizeCell.title = abTitle;
    row.appendChild(sizeCell);
    const filesCell = el("div", "cell-filesb num" + (gone ? " gone" : ""), node.kind === "file" ? "" : (gone ? node.aFiles : node.bFiles).toLocaleString());
    filesCell.title = abTitle;
    row.appendChild(filesCell);
    // bar
    const bar = el("div", "cell-bar");
    const sb = el("div", "sbar"); sb.setAttribute("aria-hidden", "true");
    const total = nodeTotal(node);
    sb.dataset.total = total;
    for (const [cls, v] of [["seg-u", node.uBytes], ["seg-m", node.modB], ["seg-a", node.add], ["seg-r", node.rem]]) {
      if (v > 0) { const s = el("i", cls); s.dataset.v = v; sb.appendChild(s); }
    }
    bar.appendChild(sb); row.appendChild(bar);
    row._node = node;
    return row;
  }
  function srText(n) {
    const k = n.kind === "dir" ? "directory" : "file";
    const map = { added: "added", removed: "removed", modified: "modified", contains: "contains changes", unchanged: "unchanged" };
    return k + ", " + map[n.status] + (n.kind === "dir" ? ", total " + human(n.bSize || n.aSize) + ", " + (n.bFiles || n.aFiles).toLocaleString() + " files" : ", " + human(n.bOwn != null ? n.bOwn : n.aOwn || 0));
  }
  function renderTree() {
    renderCrumbs();
    const tree = $("#tree"); tree.innerHTML = "";
    const rootNode = nodeAt(state.root);
    const q = state.search.trim().toLowerCase();
    let shown = 0, hidden = 0;
    const frag = document.createDocumentFragment();
    (function walk(node, path, depth) {
      const kids = sortedChildren(node);
      const maxTotal = Math.max(1, ...kids.map(nodeTotal));
      let localHidden = 0;
      for (const c of kids) {
        const cpath = (path === "/" ? "" : path) + "/" + c.name;
        if (q && !matchesSearch(c, q)) continue;
        if (!q && state.filter === "changed" && c.status === "unchanged") { hidden++; localHidden++; continue; }
        if (!q && state.filter === "added" && !(c.status === "added" || (c.kind === "dir" && c.addN > 0))) { hidden++; localHidden++; continue; }
        if (!q && state.filter === "removed" && !(c.status === "removed" || (c.kind === "dir" && c.remN > 0))) { hidden++; localHidden++; continue; }
        if (!q && state.filter === "modified" && !(c.status === "modified" || (c.kind === "dir" && c.modN > 0))) { hidden++; localHidden++; continue; }
        const row = rowFor(c, cpath, depth);
        const sb = row.querySelector(".sbar");
        const t = nodeTotal(c);
        const w = Math.max(t / maxTotal * 96, t > 0 ? 2 : 0);
        sb.style.width = Math.round(w) + "px";
        [...sb.children].forEach(seg => { seg.style.width = (Number(seg.dataset.v) / t * 100) + "%"; });
        frag.appendChild(row); shown++;
        if (c.kind === "dir" && (state.expanded.has(cpath) || q)) walk(c, cpath, depth + 1);
      }
    })(rootNode, state.root, 0);
    if (!shown) {
      const empty = el("div", "tree-empty");
      if (q) empty.innerHTML = '<div class="t">No entries match “' + esc(state.search) + '”</div><div>under ' + esc(state.root) + "</div>";
      else if (state.filter !== "all") {
        const total = rootNode.aFiles + rootNode.addN;
        empty.innerHTML = '<div class="t">No differences here</div><div>' + total.toLocaleString() + ' unchanged entries are hidden by the “Changed only” filter.</div>';
        const b = el("button", "btn-ghost", "Show all entries"); b.style.marginTop = "12px";
        b.addEventListener("click", () => { state.filter = "all"; $("#filterSel").value = "all"; renderTree(); });
        empty.appendChild(b);
      } else empty.innerHTML = '<div class="t">Empty directory</div>';
      tree.appendChild(empty);
    } else {
      tree.appendChild(frag);
      if (hidden > 0) {
        const note = el("div", "tree-note");
        note.append(hidden.toLocaleString() + " unchanged " + (hidden === 1 ? "entry" : "entries") + " hidden by filter ");
        const b = el("button", null, "Show all"); b.addEventListener("click", () => { state.filter = "all"; $("#filterSel").value = "all"; renderTree(); });
        note.appendChild(b); tree.appendChild(note);
      }
    }
    $("#showing").textContent = "showing " + shown.toLocaleString() + " of " + diffStats.total.toLocaleString() + " entries";
    $("#cmpA").textContent = "A @ layer " + (state.selA + 1);
    $("#cmpB").textContent = "B @ layer " + (state.selB + 1);
  }
  $("#filterSel").addEventListener("change", ev => { state.filter = ev.target.value; renderTree(); });
  let searchTimer;
  $("#searchInput").addEventListener("input", ev => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => { state.search = ev.target.value; renderTree(); }, 150);
  });

  // ---------- top-level render ----------
  function render() {
    $("#selectView").hidden = state.view !== "select";
    $("#browseView").hidden = state.view !== "browse";
    $("#headImgs").hidden = state.view !== "browse";
    $("#backBtn").hidden = state.view !== "browse";
    if (state.view === "select") renderSelect();
    else {
      $("#headRefA").textContent = state.slotA.ref; $("#headRefB").textContent = state.slotB.ref;
      recomputeDiff(); renderDiagram(); renderTree();
    }
  }
  window.addEventListener("resize", () => { if (state.view === "browse") drawEdges(); });
  render();
})();
