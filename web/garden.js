// Stacked-pane reader for a garden built by `garden build`.
// Data: data/index.json (titles, excerpts, nav) + data/shard-NNN.json (note
// bodies and backlinks), fetched lazily per shard.
(() => {
  "use strict";
  const $ = (s, el = document) => el.querySelector(s);
  const panesEl = $("#panes");
  const previewEl = $("#preview");
  const searchEl = $("#search"), searchInput = $("#search-input"), searchPanel = $("#search-panel");

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
    if (index.source && !entry.g) meta.push(`<a href="${esc(index.source + id + ".md")}" target="_blank" rel="noopener">Source ↗</a>`);
    const back = entry.b || [];
    const backHTML = back.length
      ? `<section class="backlinks"><h2>Links to this note · ${back.length}</h2>${back.map((b) => `<a class="backlink" href="notes/${esc(b.s)}.html" data-id="${esc(b.s)}">
          <span class="backlink-title">${esc(titleOf(b.s))}</span>
          ${b.r.map(([sec, ctx]) => `<span class="backlink-ref">${sec ? `<span class="sec">§ ${sec}</span>` : ""}${ctx}</span>`).join("")}
        </a>`).join("")}</section>`
      : `<p class="backlinks-none">No links to this note yet.</p>`;
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
        ${backHTML}
      </div>`;
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
        if (entry.m || el.querySelector(".backlinks .math")) {
          // Rendering changes heights, so re-apply an anchor jump afterwards.
          renderMath(el).then(() => {
            const at = Array.prototype.indexOf.call(panesEl.children, el);
            if (at >= 0 && stack[at]?.anchor) scrollToAnchor(el, stack[at].anchor);
          });
        }
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

  // KaTeX is loaded the first time a note with math is shown.
  let katexLoading = null;
  function loadKatex() {
    if (window.katex) return Promise.resolve();
    if (!katexLoading) {
      katexLoading = new Promise((resolve, reject) => {
        const css = document.createElement("link");
        css.rel = "stylesheet";
        css.href = "katex/katex.min.css";
        document.head.appendChild(css);
        const js = document.createElement("script");
        js.src = "katex/katex.min.js";
        js.onload = resolve;
        js.onerror = () => { katexLoading = null; reject(new Error("couldn't load KaTeX")); };
        document.head.appendChild(js);
      });
    }
    return katexLoading;
  }
  async function renderMath(root) {
    const els = root.querySelectorAll(".math:not([data-rendered])");
    if (!els.length) return;
    try { await loadKatex(); } catch { return; } // formulas stay readable as TeX
    els.forEach((el) => {
      const display = el.classList.contains("math-display");
      try {
        katex.render(el.textContent, el, { displayMode: display, throwOnError: false });
        el.dataset.rendered = "";
      } catch { /* leave the TeX source showing */ }
    });
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
    // A note's links to its own sections don't count as "open".
    panesEl.querySelectorAll("a.internal").forEach((a) => {
      const own = a.closest(".pane")?.dataset.id;
      a.classList.toggle("is-open", open.has(a.dataset.id) && a.dataset.id !== own);
    });
    searchPanel?.querySelectorAll("a[data-id]").forEach((a) => a.classList.toggle("is-open", open.has(a.dataset.id)));
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
    if (entry && entry.m) await renderMath(body);
    if (token !== previewToken || previewEl.hidden) return;
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

  // ---- search: a box in the bar. Empty, its panel shows the contents tree;
  // with a query, the notes whose title or text match. Choosing a note starts
  // a new trail: it replaces the open panes instead of adding one.
  let navHTML = "";
  let docs = [];          // [{id, title, tf (folded title), pf (folded path), back, raw, f}]
  let fullText = null;    // null: not loaded yet; true: loaded; false: unavailable
  let fullTextLoading = null;
  let active = -1;        // keyboard-highlighted link in the panel
  const MAX_HITS = 40;

  // Case- and accent-insensitive matching.
  const fold = (s) => s.normalize("NFKD").replace(/[̀-ͯ]/g, "").toLowerCase();
  // Fold s and record, for each folded char, where it came from in s.
  function foldMap(s) {
    let out = "", i = 0;
    const map = [];
    for (const ch of s) { // by code point, so surrogate pairs stay whole
      const f = fold(ch);
      for (let k = 0; k < f.length; k++) map.push(i);
      out += f;
      i += ch.length;
    }
    map.push(s.length);
    return { out, map };
  }
  // Folded text and its map back to the original. Most text folds to the
  // same length (plain or precomposed letters), where positions line up.
  function located(d) {
    if (d.f.length === d.raw.length) return { f: d.f, map: null };
    d.fm ||= foldMap(d.raw);
    return { f: d.fm.out, map: d.fm.map };
  }

  // Escape s and wrap every occurrence of the terms in <mark>.
  function highlight(s, terms) {
    const { out, map } = foldMap(s);
    const ranges = [];
    for (const w of terms) {
      for (let i = out.indexOf(w); i >= 0; i = out.indexOf(w, i + w.length)) ranges.push([map[i], map[i + w.length]]);
    }
    if (!ranges.length) return esc(s);
    ranges.sort((a, b) => a[0] - b[0]);
    let html = "", at = 0;
    for (const [a, b] of ranges) {
      if (b <= at) continue;
      const from = Math.max(a, at);
      html += esc(s.slice(at, from)) + "<mark>" + esc(s.slice(from, b)) + "</mark>";
      at = b;
    }
    return html + esc(s.slice(at));
  }

  // A line of the note around the first match, with the terms marked.
  function snippet(d, terms, phrase) {
    if (!d.raw) return "";
    const { f, map } = located(d);
    let pos = f.indexOf(phrase);
    if (pos < 0) {
      const found = terms.map((w) => f.indexOf(w)).filter((i) => i >= 0);
      pos = found.length ? Math.min(...found) : 0;
    }
    const raw = d.raw, at = map ? map[pos] : pos;
    const lineStart = raw.lastIndexOf("\n", at - 1) + 1;
    let lineEnd = raw.indexOf("\n", at);
    if (lineEnd < 0) lineEnd = raw.length;
    let start = Math.max(lineStart, at - 50);
    if (start > lineStart) { const sp = raw.indexOf(" ", start); if (sp > 0 && sp < at) start = sp + 1; }
    let end = Math.min(lineEnd, start + 170);
    if (end < lineEnd) { const sp = raw.lastIndexOf(" ", end); if (sp > at) end = sp; }
    return (start > lineStart ? "…" : "") + highlight(raw.slice(start, end), terms) + (end < lineEnd ? "…" : "");
  }

  function search(q) {
    const terms = fold(q).split(/\s+/).filter(Boolean);
    const phrase = terms.join(" ");
    const hits = [];
    for (const d of docs) {
      const { tf, f, pf } = d;
      let score;
      if (tf === phrase) score = 0;
      else if (tf.startsWith(phrase)) score = 1;
      else if (tf.includes(phrase)) score = 2;
      else if (terms.every((w) => tf.includes(w))) score = 3;
      else if (f.includes(phrase)) score = 4;
      else if (terms.every((w) => tf.includes(w) || pf.includes(w) || f.includes(w))) score = 5;
      else continue;
      hits.push([score, d]);
    }
    hits.sort((a, b) => a[0] - b[0] || b[1].back - a[1].back || a[1].title.localeCompare(b[1].title));
    return { terms, phrase, hits };
  }

  function navTree(nodes) {
    return "<ul>" + nodes.map((n) => {
      const link = n.id ? `<a class="nav-link" href="notes/${esc(n.id)}.html" data-id="${esc(n.id)}">${esc(n.t)}</a>` : `<span class="nav-label">${esc(n.t)}</span>`;
      if (!n.c) return `<li class="nav-leaf">${link}</li>`;
      return `<li><details><summary>${link}</summary>${navTree(n.c)}</details></li>`;
    }).join("") + "</ul>";
  }

  function renderPanel() {
    const q = searchInput.value.trim();
    active = -1;
    searchInput.removeAttribute("aria-activedescendant");
    if (!q) {
      searchPanel.innerHTML = navHTML
        ? `<div class="search-head">Contents</div><nav class="search-nav" aria-label="Contents">${navHTML}</nav>`
        : `<p class="search-note">Type to search ${docs.length.toLocaleString()} notes.</p>`;
      markOpenLinks();
      return;
    }
    const { terms, phrase, hits } = search(q);
    const status = fullText === null ? `<p class="search-note">Matching titles and first paragraphs; loading the full text…</p>` : "";
    if (!hits.length) {
      searchPanel.innerHTML = `<p class="search-empty">No notes match “${esc(q)}”.</p>${status}`;
      return;
    }
    searchPanel.innerHTML = `<ul class="search-hits" role="listbox" aria-label="Search results">${hits.slice(0, MAX_HITS).map(([, d], i) => {
      const folder = d.id.includes("/") ? d.id.split("/").slice(0, -1).join(" / ") : "";
      const snip = snippet(d, terms, phrase);
      return `<li role="option" id="search-hit-${i}"><a class="search-hit" href="notes/${esc(d.id)}.html" data-id="${esc(d.id)}" tabindex="-1">
        <span class="hit-title">${highlight(d.title, terms)}</span>${folder ? `<span class="hit-path">${esc(folder)}</span>` : ""}
        ${snip ? `<span class="hit-snippet">${snip}</span>` : ""}</a></li>`;
    }).join("")}</ul>${hits.length > MAX_HITS ? `<p class="search-note">${MAX_HITS} of ${hits.length.toLocaleString()} notes shown. Add a word to narrow it down.</p>` : ""}${status}`;
    markOpenLinks();
    setActive(0);
  }

  const panelLinks = () => Array.from(searchPanel.querySelectorAll("a[data-id]")).filter((a) => a.offsetParent !== null);
  function setActive(i) {
    const links = panelLinks();
    searchPanel.querySelector(".is-active")?.classList.remove("is-active");
    if (!links.length) { active = -1; return; }
    active = (i + links.length) % links.length;
    const a = links[active];
    a.classList.add("is-active");
    a.id ||= "search-item-" + active;
    searchInput.setAttribute("aria-activedescendant", a.closest("[role=option]")?.id || a.id);
    a.scrollIntoView({ block: "nearest" });
  }

  function loadFullText() {
    if (fullTextLoading) return;
    fullTextLoading = fetch("data/search.json")
      .then((r) => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.json(); })
      .then((text) => {
        for (const d of docs) {
          if (text[d.id]) { d.raw = text[d.id]; d.f = fold(d.raw); d.fm = null; }
        }
        fullText = true;
      })
      .catch(() => { fullText = false; }) // titles and first paragraphs still match
      .then(() => { if (!searchPanel.hidden && searchInput.value.trim()) renderPanel(); });
  }

  function setPanel(openIt) {
    if (openIt === !searchPanel.hidden) return;
    searchPanel.hidden = !openIt;
    searchInput.setAttribute("aria-expanded", String(openIt));
    if (openIt) { loadFullText(); renderPanel(); }
    else { active = -1; searchInput.removeAttribute("aria-activedescendant"); }
  }

  function choose(id) {
    setPanel(false);
    searchInput.blur();
    stack = [{ id }];
    render(0);
  }

  if (searchEl) {
    searchPanel.tabIndex = -1; // clicks inside keep focus within the search box
    searchInput.addEventListener("focus", () => { searchInput.select(); setPanel(true); });
    searchInput.addEventListener("input", () => { if (searchPanel.hidden) setPanel(true); else renderPanel(); });
    searchInput.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (searchPanel.hidden) { setPanel(true); return; }
        setActive(active < 0 && e.key === "ArrowUp" ? -1 : active + (e.key === "ArrowDown" ? 1 : -1));
      } else if (e.key === "Enter") {
        const a = panelLinks()[Math.max(0, active)];
        if (a) { e.preventDefault(); choose(a.dataset.id); }
      } else if (e.key === "Escape") {
        e.preventDefault();
        setPanel(false);
        searchInput.blur();
      }
    });
    searchEl.addEventListener("focusout", (e) => {
      if (!searchEl.contains(e.relatedTarget)) setPanel(false);
    });
    searchPanel.addEventListener("click", (e) => {
      const a = e.target.closest("a[data-id]");
      if (!a || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return; // new tab: the plain page
      e.preventDefault();
      choose(a.dataset.id);
    });
    addEventListener("keydown", (e) => {
      if (e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey && !/input|textarea|select/i.test(document.activeElement?.tagName) && !document.activeElement?.isContentEditable) {
        e.preventDefault();
        searchInput.focus();
      }
    });
  }

  // ---- boot
  (async () => {
    try {
      index = await (await fetch("data/index.json")).json();
    } catch (err) {
      $("#loading").textContent = "Couldn't load the notes index. Reload the page to try again.";
      return;
    }
    const count = Object.keys(index.notes).length - (index.generated_home ? 1 : 0);
    $("#stats").textContent = `${count.toLocaleString()} notes`;
    $("#site-title").addEventListener("click", (e) => { e.preventDefault(); stack = [{ id: index.home }]; render(0); });
    navHTML = index.nav && index.nav.length ? navTree(index.nav) : "";
    docs = Object.entries(index.notes)
      .filter(([id]) => !(index.generated_home && id === index.home))
      .map(([id, [title, , excerpt, back]]) => ({ id, title, tf: fold(title), pf: fold(id), back, raw: excerpt || "", f: fold(excerpt || "") }));
    const fromHash = readHash();
    stack = (fromHash.length ? fromHash : [index.home]).map((id) => ({ id }));
    render();
  })();
})();
