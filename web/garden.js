// Stacked-pane reader for a garden built by `garden build`.
// Data: data/index.json (titles, excerpts, nav) + data/shard-NNN.json (note
// bodies and backlinks), fetched lazily per shard.
(() => {
  "use strict";
  const $ = (s, el = document) => el.querySelector(s);
  const panesEl = $("#panes");
  const previewEl = $("#preview");
  const drawer = $("#drawer"), scrim = $("#scrim"), filterEl = $("#filter"), navEl = $("#nav");
  const contentsBtn = $("#contents-btn");

  let index = null;          // { title, home, notes: {id: [title, shard, excerpt, backCount]}, nav, source }
  const shards = new Map();  // shard number -> Promise<{id: entry}>
  let stack = [];            // [{id, anchor}]
  const narrow = matchMedia("(max-width: 720px)");

  // ---- URL: "#a~b~c", each id with chars outside [A-Za-z0-9_-] written as .XX
  // (hosts that only pass plain fragments through still round-trip this).
  const encId = (id) => Array.from(new TextEncoder().encode(id), (b) => {
    const c = String.fromCharCode(b);
    return /[A-Za-z0-9_-]/.test(c) ? c : "." + b.toString(16).toUpperCase().padStart(2, "0");
  }).join("");
  const decId = (s) => {
    const bytes = [];
    for (let i = 0; i < s.length; i++) {
      if (s[i] === "." && i + 2 < s.length + 0) { bytes.push(parseInt(s.slice(i + 1, i + 3), 16)); i += 2; }
      else bytes.push(s.charCodeAt(i));
    }
    return new TextDecoder().decode(new Uint8Array(bytes));
  };
  const readHash = () => location.hash.slice(1).split("~").filter(Boolean).map(decId).filter((id) => index.notes[id]);
  const writeHash = () => {
    const h = "#" + stack.map((p) => encId(p.id)).join("~");
    if (location.hash !== h) history.replaceState(null, "", h);
  };

  const loadShard = (n) => {
    if (!shards.has(n)) {
      shards.set(n, fetch(`data/shard-${String(n).padStart(3, "0")}.json`).then((r) => {
        if (!r.ok) throw new Error(`shard ${n}: HTTP ${r.status}`);
        return r.json();
      }));
    }
    return shards.get(n);
  };
  const loadNote = async (id) => (await loadShard(index.notes[id][1]))[id];

  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const titleOf = (id) => (index.notes[id] || [id])[0];

  // ---- panes
  function paneHTML(id, i, entry) {
    const meta = [];
    if (entry.d) meta.push(`<span>${esc(formatDate(entry.d))}</span>`);
    if (id.includes("/")) meta.push(`<span>${esc(id.split("/").slice(0, -1).join(" / "))}</span>`);
    if (index.source) meta.push(`<a href="${esc(index.source + id + ".md")}" target="_blank" rel="noopener">Source ↗</a>`);
    const back = entry.b || [];
    const backHTML = back.length
      ? back.map((b) => `<a class="backlink" href="notes/${esc(b.s)}.html" data-id="${esc(b.s)}">
          <span class="backlink-title">${esc(titleOf(b.s))}</span>
          ${b.r.map(([sec, ctx, label]) => `<span class="backlink-ref">${sec ? `<span class="sec">§ ${esc(sec)}</span>` : ""}${markLabel(ctx, label)}</span>`).join("")}
        </a>`).join("")
      : `<p class="backlinks-none">No notes link here yet.</p>`;
    return `
      <button class="pane-spine" type="button" tabindex="-1">${esc(titleOf(id))}</button>
      <div class="pane-scroll">
        <div class="pane-head">
          <button class="pane-back" type="button" aria-label="Back" title="Back">←</button>
          <h1 class="pane-title">${esc(titleOf(id))}</h1>
          ${i > 0 ? `<button class="pane-close" type="button" aria-label="Close note" title="Close">×</button>` : ""}
        </div>
        ${meta.length ? `<div class="pane-meta">${meta.join("")}</div>` : ""}
        <article class="note">${entry.h}</article>
        <section class="backlinks"><h2>Links to this note${back.length ? ` · ${back.length}` : ""}</h2>${backHTML}</section>
      </div>`;
  }

  function markLabel(ctx, label) {
    const e = esc(ctx);
    const l = label && esc(label);
    const i = l ? e.indexOf(l) : -1;
    return i < 0 ? e : e.slice(0, i) + "<mark>" + l + "</mark>" + e.slice(i + l.length);
  }

  function formatDate(d) {
    const m = String(d).match(/^(\d{4})-?(\d{2})-?(\d{2})/);
    if (!m) return d;
    return new Date(`${m[1]}-${m[2]}-${m[3]}T12:00:00`).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  // Bring the DOM in line with `stack`, reusing panes that haven't changed.
  async function render(focusIndex) {
    $("#loading")?.remove();
    const existing = Array.from(panesEl.children);
    let keep = 0;
    while (keep < existing.length && keep < stack.length && existing[keep].dataset.id === stack[keep].id) keep++;
    existing.slice(keep).forEach((el) => el.remove());

    for (let i = keep; i < stack.length; i++) {
      const { id } = stack[i];
      const el = document.createElement("section");
      el.className = "pane is-new";
      el.dataset.id = id;
      el.style.setProperty("--i", i);
      el.setAttribute("aria-label", titleOf(id));
      panesEl.appendChild(el);
      try {
        const entry = await loadNote(id);
        if (!el.isConnected) return; // stack changed while loading
        el.innerHTML = paneHTML(id, i, entry);
        if (entry.code) highlight(el);
      } catch (err) {
        el.innerHTML = `<div class="pane-scroll"><h1 class="pane-title">${esc(titleOf(id))}</h1><p>Couldn't load this note (${esc(err.message)}). Check your connection and reload the page.</p></div>`;
      }
      setTimeout(() => el.classList.remove("is-new"), 300);
    }
    Array.from(panesEl.children).forEach((el, i) => {
      el.style.setProperty("--i", i);
      el.querySelector(".pane-close")?.toggleAttribute("hidden", i === 0);
    });
    markOpenLinks();
    writeHash();
    document.title = stack.length > 1 ? `${titleOf(stack[stack.length - 1].id)} · ${index.title}` : index.title;
    const target = focusIndex ?? stack.length - 1;
    revealPane(target);
    const a = stack[target]?.anchor;
    if (a) scrollToAnchor(panesEl.children[target], a);
    updateCollapsed();
  }

  function highlight(el) {
    if (!window.hljs) return;
    el.querySelectorAll('pre code[class*="language-"]').forEach((c) => {
      if (c.textContent.length > 20000) return;
      const lang = (c.className.match(/language-([\w+-]+)/) || [])[1];
      if (lang && !hljs.getLanguage(lang)) return;
      try { hljs.highlightElement(c); } catch { /* leave it plain */ }
    });
  }

  // Scroll the track so pane i is fully in view (with its left neighbours' spines).
  function revealPane(i) {
    const el = panesEl.children[i];
    if (!el || narrow.matches) return;
    const spines = i * spineW();
    const left = el.offsetLeft - spines;
    const right = el.offsetLeft + el.offsetWidth - panesEl.clientWidth;
    let x = panesEl.scrollLeft;
    if (right > x) x = right;
    if (left < x) x = left;
    panesEl.scrollTo({ left: Math.max(0, x) });
  }
  const spineW = () => parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--spine-w")) || 44;

  // A pane is collapsed to its spine once the next pane slides over it.
  function updateCollapsed() {
    const kids = panesEl.children, sw = spineW(), x = panesEl.scrollLeft;
    for (let i = 0; i < kids.length; i++) {
      const next = kids[i + 1];
      const covered = !narrow.matches && next && next.offsetLeft - x < i * sw + sw + 80;
      kids[i].classList.toggle("is-collapsed", !!covered);
    }
  }

  function scrollToAnchor(pane, anchor) {
    if (!pane) return;
    const note = pane.querySelector(".note");
    if (!note) return;
    const norm = (s) => decodeURIComponent(s).normalize("NFKD").replace(/[̀-ͯ]/g, "").toLowerCase();
    let el = note.querySelector(`[id="${CSS.escape(anchor)}"]`);
    if (!el) { // tolerate accented or hand-typed anchors
      const want = norm(anchor);
      el = Array.from(note.querySelectorAll("[id]")).find((h) => norm(h.id) === want);
    }
    if (!el) return;
    const sc = pane.querySelector(".pane-scroll");
    sc.scrollTo({ top: el.offsetTop - 16, behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    el.classList.remove("flash"); void el.offsetWidth; el.classList.add("flash");
  }

  function markOpenLinks() {
    const open = new Set(stack.map((p) => p.id));
    panesEl.querySelectorAll("a.internal").forEach((a) => a.classList.toggle("is-open", open.has(a.dataset.id)));
    navEl.querySelectorAll(".nav-link").forEach((a) => a.classList.toggle("is-open", open.has(a.dataset.id)));
  }

  // Open `id` to the right of pane `from` (or as the only pane if from < 0).
  function open(id, anchor, from) {
    if (!index.notes[id]) return;
    const at = from + 1;
    if (stack[at] && stack[at].id === id) {
      stack[at].anchor = anchor;
      revealPane(at);
      if (anchor) scrollToAnchor(panesEl.children[at], anchor);
      return;
    }
    stack = stack.slice(0, Math.max(0, at)).concat({ id, anchor });
    render(stack.length - 1);
  }

  // ---- events
  panesEl.addEventListener("click", (e) => {
    const pane = e.target.closest(".pane");
    const i = pane ? Array.prototype.indexOf.call(panesEl.children, pane) : -1;
    if (e.target.closest(".pane-spine")) { revealPane(i); return; }
    if (e.target.closest(".pane-close")) { stack.splice(i, 1); render(Math.min(i, stack.length - 1)); return; }
    if (e.target.closest(".pane-back")) { stack.pop(); render(); return; }
    const a = e.target.closest("a");
    if (!a) return;
    if (a.classList.contains("missing")) { e.preventDefault(); return; }
    const id = a.dataset.id;
    if (!id || e.metaKey || e.ctrlKey || e.shiftKey) return;
    e.preventDefault();
    hidePreview();
    if (id === stack[i]?.id && a.dataset.anchor) { scrollToAnchor(pane, a.dataset.anchor); return; }
    open(id, a.dataset.anchor, i);
  });
  panesEl.addEventListener("scroll", () => requestAnimationFrame(updateCollapsed), { passive: true });
  addEventListener("resize", () => requestAnimationFrame(updateCollapsed));
  addEventListener("hashchange", () => {
    const ids = readHash();
    if (ids.length && ids.join("~") !== stack.map((p) => p.id).join("~")) {
      stack = ids.map((id) => ({ id }));
      render();
    }
  });

  // ---- hover previews: the linked note itself, rendered in a popover and
  // scrolled to the linked section. Works on every link to a note
  // (body links and backlinks). Mouse/pen only; touch taps open the pane.
  let hoverTimer = 0, hoverLink = null, previewToken = 0;
  const HOVER_DELAY = 350;
  panesEl.addEventListener("pointerover", (e) => {
    if (e.pointerType === "touch") return;
    const a = e.target.closest("a[data-id]");
    if (!a || a === hoverLink) return;
    hoverLink = a;
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(() => showPreview(a), HOVER_DELAY);
  });
  panesEl.addEventListener("pointerout", (e) => {
    const a = e.target.closest("a[data-id]");
    if (!a || a.contains(e.relatedTarget)) return; // moved between children of the same link
    hoverLink = null;
    clearTimeout(hoverTimer);
    hidePreview();
  });
  panesEl.addEventListener("scroll", hidePreview, { capture: true, passive: true });
  addEventListener("blur", hidePreview);

  async function showPreview(a) {
    const id = a.dataset.id, meta = index.notes[id];
    if (!meta || !a.isConnected) return;
    const token = ++previewToken;
    const [title, , , back] = meta;
    const anchor = a.dataset.anchor || "";
    previewEl.innerHTML = `
      <div class="preview-head">
        <div class="preview-title">${esc(title)}</div>
        ${id.includes("/") ? `<div class="preview-path">${esc(id.split("/").slice(0, -1).join(" / "))}</div>` : ""}
      </div>
      <div class="preview-body"><div class="note"><p class="preview-loading">Loading…</p></div></div>
      <div class="preview-meta">${back} backlink${back === 1 ? "" : "s"} · click to open beside this note</div>`;
    previewEl.hidden = false;
    placePreview(a);
    let entry;
    try { entry = await loadNote(id); } catch { entry = null; }
    if (token !== previewToken || previewEl.hidden) return; // pointer moved on
    const body = previewEl.querySelector(".preview-body");
    body.firstElementChild.innerHTML = entry ? entry.h : "<p>Couldn't load this note.</p>";
    body.querySelectorAll("img").forEach((img) => { img.loading = "eager"; });
    if (entry && entry.code) highlight(body);
    if (anchor) {
      const h = body.querySelector(`[id="${CSS.escape(anchor)}"]`);
      if (h) { body.scrollTop += h.getBoundingClientRect().top - body.getBoundingClientRect().top - 10; h.classList.add("preview-target"); }
    }
    placePreview(a);
  }

  // Beside the link: below it if there's room, otherwise above; clamped to the window.
  function placePreview(a) {
    const r = a.getBoundingClientRect(), p = previewEl.getBoundingClientRect(), gap = 10;
    let top = r.bottom + gap;
    if (top + p.height > innerHeight - 8 && r.top - p.height - gap > 8) top = r.top - p.height - gap;
    top = Math.min(Math.max(8, top), Math.max(8, innerHeight - p.height - 8));
    const left = Math.min(Math.max(8, r.left - 24), innerWidth - p.width - 8);
    previewEl.style.top = `${top}px`;
    previewEl.style.left = `${Math.max(8, left)}px`;
  }
  function hidePreview() {
    if (previewEl.hidden) return;
    previewToken++;
    previewEl.hidden = true;
    previewEl.innerHTML = "";
  }

  // ---- contents drawer
  function setDrawer(openIt) {
    drawer.hidden = scrim.hidden = !openIt;
    contentsBtn.setAttribute("aria-expanded", String(openIt));
    if (openIt) { markOpenLinks(); setTimeout(() => filterEl.focus(), 0); }
  }
  contentsBtn.addEventListener("click", () => setDrawer(drawer.hidden));
  scrim.addEventListener("click", () => setDrawer(false));
  addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !drawer.hidden) setDrawer(false);
    if (e.key === "/" && drawer.hidden && !/input|textarea/i.test(document.activeElement?.tagName)) { e.preventDefault(); setDrawer(true); }
  });
  navEl.addEventListener("click", (e) => {
    const a = e.target.closest("a.nav-link");
    if (!a) return;
    e.preventDefault();
    setDrawer(false);
    // From the contents, a note opens beside the current one.
    open(a.dataset.id, null, stack.length - 1);
  });

  function navTree(nodes) {
    return "<ul>" + nodes.map((n) => {
      const link = n.id ? `<a class="nav-link" href="notes/${esc(n.id)}.html" data-id="${esc(n.id)}">${esc(n.t)}</a>` : `<span class="nav-label">${esc(n.t)}</span>`;
      if (!n.c) return `<li class="nav-leaf">${link}</li>`;
      return `<li><details><summary>${link}</summary>${navTree(n.c)}</details></li>`;
    }).join("") + "</ul>";
  }
  let navHTML = "";
  filterEl.addEventListener("input", () => {
    const q = filterEl.value.trim().toLowerCase();
    if (!q) { navEl.innerHTML = navHTML; markOpenLinks(); return; }
    const hits = [];
    for (const [id, [t]] of Object.entries(index.notes)) {
      const tl = t.toLowerCase();
      const i = tl.indexOf(q);
      const score = i === 0 ? 0 : i > 0 ? 1 : id.toLowerCase().includes(q) ? 2 : -1;
      if (score >= 0) hits.push([score, t, id]);
    }
    hits.sort((a, b) => a[0] - b[0] || a[1].localeCompare(b[1]));
    navEl.innerHTML = hits.length
      ? "<ul>" + hits.slice(0, 80).map(([, t, id]) => `<li class="nav-leaf"><a class="nav-link" href="notes/${esc(id)}.html" data-id="${esc(id)}">${esc(t)}</a>${id.includes("/") ? `<span class="result-path">${esc(id)}</span>` : ""}</li>`).join("") + "</ul>"
      : `<p class="empty">No note titles match “${esc(filterEl.value)}”.</p>`;
    markOpenLinks();
  });
  filterEl.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    const first = navEl.querySelector("a.nav-link");
    if (first) first.click();
  });

  // ---- boot
  (async () => {
    try {
      index = await (await fetch("data/index.json")).json();
    } catch (err) {
      $("#loading").textContent = "Couldn't load the notes index. Reload the page to try again.";
      return;
    }
    const count = Object.keys(index.notes).length;
    $("#stats").textContent = `${count.toLocaleString()} notes`;
    $("#site-title").addEventListener("click", (e) => { e.preventDefault(); stack = [{ id: index.home }]; render(0); });
    navHTML = index.nav && index.nav.length ? navTree(index.nav) : "";
    navEl.innerHTML = navHTML;
    const fromHash = readHash();
    stack = (fromHash.length ? fromHash : [index.home]).map((id) => ({ id }));
    render();
  })();
})();
