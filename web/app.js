/* sundial playground — vanilla JS front end over the WebAssembly engine.
 *
 * The engine exposes a global `sundial` object (see cmd/sundial-wasm). It has
 * one method, compute(config), which rebuilds a fresh Timer from the whole
 * configuration and returns the computed state. There is no network and no
 * framework, and the bridge holds no state — the page below is authoritative.
 *
 * Instants cross the boundary as civil-time strings in the schedule's zone
 * ("2026-01-09T16:00:00"). On this side we model civil time as "pseudo-UTC":
 * a civil wall-clock is stored as the millisecond value Date.UTC(...) would
 * give it, so every day is a flat 24h and layout maths is linear. The engine
 * resolves each string in the real zone, so the numbers stay DST-correct even
 * though the pixel timeline is a plain civil calendar.
 */

(function () {
  "use strict";

  const NS = "http://www.w3.org/2000/svg";
  const DAY_MS = 86400000;
  const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  // Editor row order: Monday first, weekend last. Values are time.Weekday indices.
  const WK_ORDER = [1, 2, 3, 4, 5, 6, 0];

  let C = null; // the sundial engine (window.sundial)
  let lastResp = null;

  const state = {
    tz: "America/New_York",
    week: makeWeekdays("09:00", "17:00"), // 7 arrays, index 0=Sun..6=Sat
    holidays: [], // ["YYYY-MM-DD", ...]
    budgetHours: 8,
    levels: [
      { name: "warn", hours: 4 },
      { name: "breach", hours: 8 },
    ],
    start: strToCivil("2026-01-09T16:00:00"),
    now: strToCivil("2026-01-12T11:00:00"),
    pauses: [], // [{ startMs, endMs|null }]
    range: { lo: 0, hi: DAY_MS * 3 },
  };

  // ---- tiny helpers --------------------------------------------------------

  function $(id) { return document.getElementById(id); }
  function el(tag, attrs) {
    const e = document.createElementNS(NS, tag);
    for (const k in attrs) e.setAttribute(k, attrs[k]);
    return e;
  }
  function svgText(x, y, cls, text, extra) {
    const a = Object.assign({ x, y }, extra || {});
    if (cls) a.class = cls;
    const t = el("text", a);
    t.textContent = text;
    return t;
  }
  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }
  function pad(n) { return String(n).padStart(2, "0"); }
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  }

  // ---- civil <-> string ----------------------------------------------------

  // civilToStr renders a pseudo-UTC millisecond value as a civil-time string the
  // engine parses in the schedule zone.
  function civilToStr(ms) {
    const d = new Date(ms);
    return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}` +
      `T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`;
  }
  function strToCivil(s) {
    const m = String(s).match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?/);
    if (!m) return NaN;
    return Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], m[6] ? +m[6] : 0);
  }
  // civilToLocalInput renders a pseudo-UTC ms as the "YYYY-MM-DDTHH:MM" a
  // datetime-local input expects (its value string is read straight back as civil).
  function civilToLocalInput(ms) { return civilToStr(ms).slice(0, 16); }

  function floorDay(ms) { return Math.floor(ms / DAY_MS) * DAY_MS; }
  function weekdayOf(ms) { return new Date(ms).getUTCDay(); }
  function dateStr(ms) {
    const d = new Date(ms);
    return `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;
  }
  function hmToSec(hm) {
    const p = String(hm).split(":");
    return (+p[0]) * 3600 + (+(p[1] || 0)) * 60 + (+(p[2] || 0));
  }

  // fmtDur renders a whole-second duration as "4h 30m" / "45m" / "8h" / "0m".
  function fmtDur(sec) {
    sec = Math.max(0, Math.round(sec));
    const h = Math.floor(sec / 3600);
    const m = Math.round((sec % 3600) / 60);
    if (h === 0) return `${m}m`;
    if (m === 0) return `${h}h`;
    return `${h}h ${m}m`;
  }
  // fmtNice renders a civil string as "Mon Jan 12, 16:00".
  function fmtNice(s) {
    const ms = strToCivil(s);
    if (!isFinite(ms)) return "—";
    const d = new Date(ms);
    return `${DOW[d.getUTCDay()]} ${MON[d.getUTCMonth()]} ${d.getUTCDate()}, ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
  }
  // fmtShort renders "Fri 16:00".
  function fmtShort(ms) {
    const d = new Date(ms);
    return `${DOW[d.getUTCDay()]} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
  }

  // makeWeekdays builds a 7-day week with the given window Mon–Fri, weekend empty.
  function makeWeekdays(open, close) {
    const w = [[], [], [], [], [], [], []];
    for (let d = 1; d <= 5; d++) w[d] = [{ open, close }];
    return w;
  }

  // ---- engine call ---------------------------------------------------------

  function request() {
    return {
      schedule: {
        tz: state.tz,
        week: state.week.map((day) => day.map((w) => ({ open: w.open, close: w.close }))),
        holidays: state.holidays.slice(),
      },
      budgetSeconds: Math.round(state.budgetHours * 3600),
      levels: state.levels.map((l) => ({ name: l.name, atSeconds: Math.round(l.hours * 3600) })),
      start: civilToStr(state.start),
      now: civilToStr(state.now),
      pauses: state.pauses.map((p) => ({
        start: civilToStr(p.startMs),
        end: p.endMs == null ? "" : civilToStr(p.endMs),
      })),
    };
  }
  function evaluate() { return C.compute(request()); }

  function showErr(id, resp) {
    const b = $(id);
    if (!b) return false;
    if (resp && resp.error) { b.textContent = resp.error; b.hidden = false; return true; }
    b.hidden = true;
    return false;
  }

  // refresh runs one compute and repaints everything from the single result.
  function refresh() {
    const resp = evaluate();
    const bad = showErr("timeline-err", resp);
    showErr("escalation-err", resp);
    showErr("pause-err", resp);
    if (bad) return;
    lastResp = resp;
    renderTimeline(resp);
    renderReadouts(resp);
    renderLevels(resp);
    renderPauses(resp);
    renderDST(resp);
  }

  // recomputeRange sizes the visible window to cover start, now, due, pauses and
  // level crossings. Called on a config change, never mid-drag, so the timeline
  // never shifts under the cursor.
  function recomputeRange() {
    const r = evaluate();
    let lo = Math.min(state.start, state.now);
    let hi = Math.max(state.start, state.now);
    if (r && !r.error) {
      const due = strToCivil(r.dueAt);
      if (isFinite(due)) { lo = Math.min(lo, due); hi = Math.max(hi, due); }
      for (const lv of r.levels || []) {
        if (lv.crossingAt) { const c = strToCivil(lv.crossingAt); if (isFinite(c)) { lo = Math.min(lo, c); hi = Math.max(hi, c); } }
      }
    }
    for (const p of state.pauses) {
      lo = Math.min(lo, p.startMs);
      hi = Math.max(hi, p.endMs == null ? p.startMs : p.endMs);
    }
    lo = floorDay(lo) - DAY_MS / 2;
    hi = floorDay(hi) + DAY_MS + DAY_MS / 2;
    if (hi - lo < 3 * DAY_MS) hi = lo + 3 * DAY_MS;
    state.range = { lo, hi };
  }

  function onConfigChange() { recomputeRange(); refresh(); }

  // =========================================================================
  //  TIMELINE
  // =========================================================================

  const VBW = 960, VBH = 172;
  const PLOT_L = 14, PLOT_R = VBW - 14, PLOT_W = PLOT_R - PLOT_L;
  const BAND_Y = 62, BAND_H = 68, BAND_B = BAND_Y + BAND_H;

  function xOf(ms) {
    const R = state.range;
    return PLOT_L + ((ms - R.lo) / (R.hi - R.lo)) * PLOT_W;
  }
  function clampMs(ms) {
    const R = state.range;
    return Math.max(R.lo, Math.min(R.hi, ms));
  }

  function renderTimeline(resp) {
    const svg = $("timeline-svg");
    clear(svg);
    const R = state.range;

    // 1) dark non-working band background.
    svg.appendChild(el("rect", { x: PLOT_L, y: BAND_Y, width: PLOT_W, height: BAND_H, rx: 6, class: "tl-off" }));

    // 2) per-day shading, separators and labels.
    for (let d = floorDay(R.lo); d < R.hi; d += DAY_MS) {
      const x0 = Math.max(PLOT_L, xOf(d));
      const x1 = Math.min(PLOT_R, xOf(d + DAY_MS));
      if (x1 <= x0) continue;
      const wd = weekdayOf(d);
      const isHol = state.holidays.indexOf(dateStr(d)) >= 0;
      const isWeekend = wd === 0 || wd === 6;

      if (d > R.lo) svg.appendChild(el("line", { x1: xOf(d), y1: BAND_Y, x2: xOf(d), y2: BAND_B, class: "tl-sep" }));

      if (isHol) {
        svg.appendChild(el("rect", { x: x0, y: BAND_Y, width: x1 - x0, height: BAND_H, class: "tl-holiday" }));
        svg.appendChild(svgText((x0 + x1) / 2, BAND_B - 8, "tl-closed-tag", "holiday", { "text-anchor": "middle" }));
      } else {
        // light working windows.
        for (const w of state.week[wd]) {
          const ox = xOf(d + hmToSec(w.open) * 1000);
          const cx = xOf(d + hmToSec(w.close) * 1000);
          const wx0 = Math.max(PLOT_L, ox), wx1 = Math.min(PLOT_R, cx);
          if (wx1 > wx0) {
            svg.appendChild(el("rect", { x: wx0, y: BAND_Y + 1, width: wx1 - wx0, height: BAND_H - 2, class: "tl-on" }));
          }
        }
        if (state.week[wd].length === 0) {
          svg.appendChild(svgText((x0 + x1) / 2, BAND_B - 8, "tl-closed-tag", isWeekend ? "weekend" : "closed", { "text-anchor": "middle" }));
        }
      }
      // day label above the band.
      const dd = new Date(d);
      const lbl = `${DOW[wd]} ${dd.getUTCMonth() + 1}/${dd.getUTCDate()}`;
      svg.appendChild(svgText((x0 + x1) / 2, 50, "tl-daylabel" + (isWeekend ? " weekend" : ""), lbl, { "text-anchor": "middle" }));
    }

    // 3) frame around the band.
    svg.appendChild(el("rect", { x: PLOT_L, y: BAND_Y, width: PLOT_W, height: BAND_H, rx: 6, class: "tl-frame" }));

    // 4) pause spans (translucent), drawn over the band.
    for (const p of resp.pauses || []) {
      const ps = strToCivil(p.start);
      const pe = p.open ? state.now : strToCivil(p.end);
      const a = Math.max(PLOT_L, xOf(ps)), b = Math.min(PLOT_R, xOf(pe));
      if (b > a) {
        svg.appendChild(el("rect", { x: a, y: BAND_Y + 1, width: b - a, height: BAND_H - 2, class: "tl-pause" }));
        if (b - a > 42) svg.appendChild(svgText((a + b) / 2, BAND_Y + 16, "tl-pause-tag", p.open ? "paused (open)" : "paused", { "text-anchor": "middle" }));
      }
    }

    // 5) level crossing ticks under the band.
    (resp.levels || []).forEach((lv) => {
      if (!lv.crossingAt) return;
      const cx = xOf(strToCivil(lv.crossingAt));
      if (cx < PLOT_L - 2 || cx > PLOT_R + 2) return;
      const cls = lv.name === "breach" ? "breach" : "warn";
      const state2 = lv.reached ? cls : "pending";
      svg.appendChild(el("path", {
        d: `M${cx - 5},${BAND_B + 9} L${cx + 5},${BAND_B + 9} L${cx},${BAND_B + 1} Z`,
        class: "tl-level " + cls + (lv.reached ? "" : " pending"),
      }));
      svg.appendChild(svgText(cx, BAND_B + 21, "tl-level-label " + state2, lv.name, { "text-anchor": "middle" }));
    });

    // 6) start / due markers with flag pills.
    drawMarker(svg, strToCivil(resp.start), "start", "start", "flag-start", "tl-start-line");
    drawMarker(svg, strToCivil(resp.dueAt), "due", "due " + fmtShort(strToCivil(resp.dueAt)), "flag-due", "tl-due-line");

    // 7) now handle (draggable).
    const nx = xOf(state.now);
    svg.appendChild(el("line", { x1: nx, y1: BAND_Y - 8, x2: nx, y2: BAND_B + 26, class: "tl-now-line" }));
    svg.appendChild(el("circle", { cx: nx, cy: BAND_Y - 8, r: 7, class: "tl-now-grip" }));
    drawFlag(svg, nx, 18, "now", "flag-now");
  }

  function drawMarker(svg, ms, kind, label, flagCls, lineCls) {
    if (!isFinite(ms)) return;
    const x = xOf(ms);
    if (x < PLOT_L - 40 || x > PLOT_R + 60) return;
    svg.appendChild(el("line", { x1: x, y1: BAND_Y - 4, x2: x, y2: BAND_B + 4, class: lineCls }));
    drawFlag(svg, x, 34, label, flagCls);
  }

  // drawFlag draws a small rounded pill with white text, kept inside the plot.
  function drawFlag(svg, x, y, label, cls) {
    const w = 12 + label.length * 6.0;
    let fx = x - w / 2;
    fx = Math.max(PLOT_L, Math.min(PLOT_R - w, fx));
    svg.appendChild(el("rect", { x: fx, y: y - 12, width: w, height: 17, rx: 4, class: "flag-pill " + cls }));
    svg.appendChild(svgText(fx + w / 2, y, "flag-text", label, { "text-anchor": "middle" }));
  }

  // ---- now dragging --------------------------------------------------------

  let rafPending = false;
  function scheduleRefresh() {
    if (rafPending) return;
    rafPending = true;
    requestAnimationFrame(() => { rafPending = false; refresh(); });
  }
  function clientXToMs(clientX) {
    const svg = $("timeline-svg");
    const rect = svg.getBoundingClientRect();
    const vx = ((clientX - rect.left) / rect.width) * VBW;
    let ms = state.range.lo + ((vx - PLOT_L) / PLOT_W) * (state.range.hi - state.range.lo);
    ms = Math.round(ms / (5 * 60000)) * 5 * 60000; // snap to 5 minutes
    return clampMs(ms);
  }
  function wireDrag() {
    const svg = $("timeline-svg");
    let dragging = false;
    const move = (e) => { if (!dragging) return; state.now = clientXToMs(e.clientX); scheduleRefresh(); };
    svg.addEventListener("pointerdown", (e) => {
      dragging = true;
      svg.setPointerCapture(e.pointerId);
      state.now = clientXToMs(e.clientX);
      refresh();
      e.preventDefault();
    });
    svg.addEventListener("pointermove", move);
    svg.addEventListener("pointerup", (e) => { dragging = false; try { svg.releasePointerCapture(e.pointerId); } catch (_) {} });
    svg.addEventListener("pointercancel", () => { dragging = false; });
  }

  // =========================================================================
  //  READOUTS + PROGRESS
  // =========================================================================

  function renderReadouts(resp) {
    $("el-elapsed").textContent = fmtDur(resp.elapsedSeconds);
    $("el-remaining").textContent = resp.breached ? "0m" : fmtDur(resp.remainingSeconds);
    $("el-due").textContent = fmtNice(resp.dueAt);

    const card = $("status-card");
    card.classList.remove("ok", "breached", "paused");
    let status;
    if (resp.breached) { status = "Breached"; card.classList.add("breached"); }
    else if (resp.paused) { status = "Paused"; card.classList.add("paused"); }
    else { status = "On track"; card.classList.add("ok"); }
    $("el-status").textContent = status;

    const budget = resp.budgetSeconds || 1;
    const pct = Math.max(0, Math.min(1, resp.elapsedSeconds / budget));
    const fill = $("prog-fill");
    fill.style.width = (pct * 100).toFixed(1) + "%";
    fill.classList.toggle("breached", resp.breached);
    $("prog-label").textContent =
      `${fmtDur(resp.elapsedSeconds)} / ${fmtDur(resp.budgetSeconds)} (${Math.round(pct * 100)}%)`;

    const frozen = resp.paused ? ` <span class="frozen">(frozen — paused)</span>` : "";
    $("now-readout").innerHTML =
      `Now <b>${fmtNice(resp.now)}</b> &middot; elapsed working <b>${fmtDur(resp.elapsedSeconds)}</b>${frozen}`;
  }

  // =========================================================================
  //  ESCALATION LEVELS
  // =========================================================================

  function levelColor(name) { return name === "breach" ? "var(--neg)" : name === "warn" ? "var(--warn)" : "var(--accent)"; }

  function buildLevelEditor() {
    const host = $("level-editor");
    clear(host);
    state.levels.forEach((lv, i) => {
      const row = document.createElement("div");
      row.className = "lv-row";
      const sw = document.createElement("span");
      sw.className = "sw"; sw.style.background = levelColor(lv.name);
      row.appendChild(sw);

      const name = document.createElement("input");
      name.type = "text"; name.value = lv.name; name.setAttribute("aria-label", "level name");
      name.addEventListener("input", () => { lv.name = name.value; sw.style.background = levelColor(lv.name); refresh(); });
      row.appendChild(name);

      const hrs = document.createElement("input");
      hrs.type = "number"; hrs.min = "0"; hrs.step = "0.5"; hrs.value = String(lv.hours);
      hrs.setAttribute("aria-label", "level threshold hours");
      hrs.addEventListener("input", () => { const v = Number(hrs.value); if (!isNaN(v)) { lv.hours = v; onConfigChange(); } });
      row.appendChild(hrs);

      const unit = document.createElement("span");
      unit.className = "unit"; unit.textContent = "working h";
      row.appendChild(unit);

      const del = document.createElement("button");
      del.className = "lv-del"; del.type = "button"; del.textContent = "×"; del.title = "Remove level";
      del.addEventListener("click", () => { state.levels.splice(i, 1); buildLevelEditor(); onConfigChange(); });
      row.appendChild(del);

      host.appendChild(row);
    });
  }

  function renderLevels(resp) {
    const host = $("fire-badges");
    clear(host);
    const levels = resp.levels || [];
    if (levels.length === 0) {
      host.innerHTML = `<p class="chips-empty">No escalation levels. Add one above.</p>`;
      return;
    }
    for (const lv of levels) {
      const cls = lv.name === "breach" ? "breach" : lv.name === "warn" ? "warn" : "warn";
      const badge = document.createElement("div");
      badge.className = "fire-badge " + cls + (lv.reached ? " lit" : "");
      const when = lv.crossingAt
        ? `<span class="st">${lv.reached ? "fired at" : "will fire"}</span>${escapeHtml(fmtNice(lv.crossingAt))}`
        : `<span class="st">threshold</span>—`;
      badge.innerHTML =
        `<span class="dot"></span>` +
        `<span class="fb-name">${escapeHtml(lv.name)}</span>` +
        `<span class="fb-thr">at ${fmtDur(lv.atSeconds)}</span>` +
        `<span class="fb-when">${when}</span>`;
      host.appendChild(badge);
    }
  }

  // =========================================================================
  //  PAUSE / RESUME
  // =========================================================================

  function buildPauseEditor() {
    const host = $("pause-editor");
    clear(host);
    if (state.pauses.length === 0) {
      host.innerHTML = `<p class="chips-empty">No pauses. Add one to freeze the clock while waiting on the customer.</p>`;
      return;
    }
    state.pauses.forEach((p, i) => {
      const row = document.createElement("div");
      row.className = "pz-row";

      const sLab = document.createElement("span"); sLab.className = "pz-lab"; sLab.textContent = "pause"; row.appendChild(sLab);
      const startIn = document.createElement("input");
      startIn.type = "datetime-local"; startIn.value = civilToLocalInput(p.startMs);
      startIn.setAttribute("aria-label", "pause start");
      startIn.addEventListener("input", () => { const v = strToCivil(startIn.value); if (isFinite(v)) { p.startMs = v; onConfigChange(); } });
      row.appendChild(startIn);

      const arrow = document.createElement("span"); arrow.className = "pz-lab"; arrow.textContent = "→"; row.appendChild(arrow);

      const endIn = document.createElement("input");
      endIn.type = "datetime-local";
      endIn.value = p.endMs == null ? "" : civilToLocalInput(p.endMs);
      endIn.disabled = p.endMs == null;
      endIn.setAttribute("aria-label", "pause end");
      endIn.addEventListener("input", () => { const v = strToCivil(endIn.value); if (isFinite(v)) { p.endMs = v; onConfigChange(); } });
      row.appendChild(endIn);

      const openWrap = document.createElement("label"); openWrap.className = "pz-open";
      const openChk = document.createElement("input"); openChk.type = "checkbox"; openChk.checked = p.endMs == null;
      openChk.addEventListener("change", () => {
        if (openChk.checked) { p.endMs = null; endIn.disabled = true; endIn.value = ""; }
        else { p.endMs = p.startMs + 3 * 3600 * 1000; endIn.disabled = false; endIn.value = civilToLocalInput(p.endMs); }
        onConfigChange();
      });
      openWrap.appendChild(openChk);
      openWrap.appendChild(document.createTextNode("open"));
      row.appendChild(openWrap);

      const del = document.createElement("button");
      del.className = "pz-del"; del.type = "button"; del.textContent = "×"; del.title = "Remove pause";
      del.addEventListener("click", () => { state.pauses.splice(i, 1); buildPauseEditor(); onConfigChange(); });
      row.appendChild(del);

      host.appendChild(row);
    });
  }

  function renderPauses(resp) {
    const host = $("pause-readout");
    clear(host);
    const pauses = resp.pauses || [];
    if (pauses.length === 0) return;
    pauses.forEach((p, i) => {
      const fact = document.createElement("div");
      const span = p.open
        ? `${fmtShort(strToCivil(p.start))} → open`
        : `${fmtShort(strToCivil(p.start))} → ${fmtShort(strToCivil(p.end))}`;
      let msg, cls;
      if (p.open) {
        msg = `<b>Pause ${i + 1}</b> ${span} · open, freezing elapsed at the pause instant.`;
        cls = "warn";
      } else if (p.workingSeconds === 0) {
        msg = `<b>Pause ${i + 1}</b> ${span} · holds <b>0</b> working time — due shifts nothing.`;
        cls = "zero";
      } else {
        msg = `<b>Pause ${i + 1}</b> ${span} · holds <b>${fmtDur(p.workingSeconds)}</b> working time — due shifts later by that much.`;
        cls = "warn";
      }
      fact.className = "pz-fact " + cls;
      fact.innerHTML = msg;
      host.appendChild(fact);
    });
  }

  // =========================================================================
  //  DST PANEL
  // =========================================================================

  function renderDST(resp) {
    $("dst-due").textContent = fmtNice(resp.dueAt);
    $("dst-wall").textContent = fmtDur(resp.wallSpanSeconds);
    $("dst-work").textContent = fmtDur(resp.budgetSeconds);
    const wallH = Math.round(resp.wallSpanSeconds / 3600);
    const workH = Math.round(resp.budgetSeconds / 3600);
    const diff = wallH - workH;
    let note;
    if (state.tz === "America/New_York" && dateStr(state.start) === "2025-03-07") {
      note = `The budget of <b>${fmtDur(resp.budgetSeconds)}</b> working time lands on <b>${fmtNice(resp.dueAt)}</b> — ` +
        `the civil instant the arithmetic expects. Yet the real wall-clock span is <b>${fmtDur(resp.wallSpanSeconds)}</b>, ` +
        `one hour shorter than a naive count, because the clocks jumped forward at 02:00 Sunday. ` +
        `The window is civil-time, so 09:00–17:00 stays eight working hours either way.`;
    } else {
      note = `Due is <b>${fmtNice(resp.dueAt)}</b>. The clock counts <b>${fmtDur(resp.budgetSeconds)}</b> of working ` +
        `time across a real wall-clock span of <b>${fmtDur(resp.wallSpanSeconds)}</b>` +
        (diff !== 0 ? ` (a ${Math.abs(diff)}h offset from a daylight-saving change inside the span).` : `.`) +
        ` Load the spring-forward story to see the one-hour DST gap.`;
    }
    $("dst-note").innerHTML = note;
  }

  // =========================================================================
  //  SCHEDULE EDITOR (week + holidays)
  // =========================================================================

  function buildWeekEditor() {
    const host = $("week-editor");
    clear(host);
    for (const wd of WK_ORDER) {
      const win = state.week[wd][0] || null;
      const row = document.createElement("div");
      row.className = "wk-row" + (win ? "" : " off");

      const day = document.createElement("span"); day.className = "wk-day"; day.textContent = DOW[wd]; row.appendChild(day);

      const chk = document.createElement("input"); chk.type = "checkbox"; chk.checked = !!win;
      chk.setAttribute("aria-label", DOW[wd] + " working");
      chk.addEventListener("change", () => {
        if (chk.checked) state.week[wd] = [{ open: "09:00", close: "17:00" }];
        else state.week[wd] = [];
        buildWeekEditor(); onConfigChange();
      });
      row.appendChild(chk);

      if (win) {
        const openIn = document.createElement("input");
        openIn.type = "time"; openIn.value = win.open;
        openIn.addEventListener("input", () => { if (openIn.value) { win.open = openIn.value; onConfigChange(); } });
        row.appendChild(openIn);
        const dash = document.createElement("span"); dash.className = "dash"; dash.textContent = "–"; row.appendChild(dash);
        const closeIn = document.createElement("input");
        closeIn.type = "time"; closeIn.value = win.close;
        closeIn.addEventListener("input", () => { if (closeIn.value) { win.close = closeIn.value; onConfigChange(); } });
        row.appendChild(closeIn);
      } else {
        const closed = document.createElement("span"); closed.className = "wk-closed"; closed.textContent = "closed"; row.appendChild(closed);
      }
      host.appendChild(row);
    }
  }

  function buildHolidayChips() {
    const host = $("hol-chips");
    clear(host);
    if (state.holidays.length === 0) { host.innerHTML = `<span class="chips-empty">None</span>`; return; }
    state.holidays.slice().sort().forEach((h) => {
      const chip = document.createElement("span");
      chip.className = "chip";
      chip.appendChild(document.createTextNode(h));
      const x = document.createElement("button");
      x.type = "button"; x.textContent = "×"; x.title = "Remove holiday";
      x.addEventListener("click", () => { state.holidays = state.holidays.filter((d) => d !== h); buildHolidayChips(); onConfigChange(); });
      chip.appendChild(x);
      host.appendChild(chip);
    });
  }

  // =========================================================================
  //  SCENARIOS
  // =========================================================================

  function buildScenarioButtons() {
    const host = $("scenario-buttons");
    clear(host);
    const list = C.scenarios || [];
    for (const sc of list) {
      const b = document.createElement("button");
      b.className = "btn"; b.type = "button"; b.textContent = sc.title; b.dataset.name = sc.name;
      b.addEventListener("click", () => loadScenario(sc.name, b));
      host.appendChild(b);
    }
  }

  function loadScenario(name, btn) {
    const sc = C.loadScenario(name);
    if (!sc || sc.error) return;
    document.querySelectorAll("#scenario-buttons .btn").forEach((b) => b.classList.toggle("active", b === btn || b.dataset.name === name));
    const note = $("scenario-note");
    note.innerHTML = `<b>${escapeHtml(sc.title)}.</b> ${escapeHtml(sc.note)}`;
    note.hidden = false;
    applyScenario(sc);
    const panel = $("panel-" + (sc.panel || "timeline"));
    if (panel) panel.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  function applyScenario(sc) {
    const cfg = sc.config || {};
    state.tz = cfg.tz || "UTC";
    state.week = makeWeekdays(cfg.weekOpen || "09:00", cfg.weekClose || "17:00");
    state.holidays = (cfg.holidays || []).slice();
    state.budgetHours = cfg.budgetHours != null ? cfg.budgetHours : 8;
    state.levels = (cfg.levels || []).map((l) => ({ name: l.name, hours: l.hours }));
    state.start = strToCivil(cfg.start);
    state.now = strToCivil(cfg.now);
    state.pauses = (cfg.pauses || []).map((p) => ({ startMs: strToCivil(p.start), endMs: p.end ? strToCivil(p.end) : null }));

    $("tz-select").value = state.tz;
    $("budget-hours").value = String(state.budgetHours);
    buildWeekEditor();
    buildHolidayChips();
    buildLevelEditor();
    buildPauseEditor();
    onConfigChange();
  }

  // =========================================================================
  //  WIRING + BOOT
  // =========================================================================

  function wire() {
    $("tz-select").addEventListener("change", (e) => { state.tz = e.target.value; onConfigChange(); });
    $("budget-hours").addEventListener("input", (e) => { const v = Number(e.target.value); if (!isNaN(v)) { state.budgetHours = v; onConfigChange(); } });

    $("hol-add").addEventListener("click", () => {
      const v = $("hol-input").value;
      if (v && state.holidays.indexOf(v) < 0) { state.holidays.push(v); buildHolidayChips(); onConfigChange(); }
    });

    $("level-add").addEventListener("click", () => {
      const maxH = state.levels.reduce((m, l) => Math.max(m, l.hours), 0);
      state.levels.push({ name: "page", hours: maxH + 4 });
      buildLevelEditor(); onConfigChange();
    });

    $("pause-add").addEventListener("click", () => {
      const s = clampMs(state.start + 2 * 3600 * 1000);
      state.pauses.push({ startMs: s, endMs: s + 3 * 3600 * 1000 });
      buildPauseEditor(); onConfigChange();
    });

    $("dst-load").addEventListener("click", () => {
      const btn = document.querySelector('#scenario-buttons .btn[data-name="spring-forward"]');
      loadScenario("spring-forward", btn);
    });

    wireDrag();
  }

  let started = false;
  function start() {
    if (started) return;
    started = true;
    C = window.sundial;

    buildWeekEditor();
    buildHolidayChips();
    buildLevelEditor();
    buildPauseEditor();
    buildScenarioButtons();
    wire();

    $("boot").classList.add("hidden");

    // Open on the flagship story.
    const firstBtn = document.querySelector('#scenario-buttons .btn[data-name="support-8h"]') ||
      document.querySelector("#scenario-buttons .btn");
    loadScenario("support-8h", firstBtn);
  }

  function boot() {
    if (!window.Go) { fail("wasm_exec.js failed to load."); return; }
    const go = new Go();
    const url = "sundial.wasm";
    window.__sundialOnReady = () => start();
    const inst = (obj) => { go.run(obj.instance); if (window.__sundialReady) start(); };
    if (WebAssembly.instantiateStreaming) {
      WebAssembly.instantiateStreaming(fetch(url), go.importObject).then(inst).catch(() => fallback(go, url, inst));
    } else {
      fallback(go, url, inst);
    }
  }
  function fallback(go, url, inst) {
    fetch(url).then((r) => r.arrayBuffer()).then((buf) => WebAssembly.instantiate(buf, go.importObject)).then(inst)
      .catch((e) => fail("Could not load the wasm module: " + e));
  }
  function fail(msg) { const b = $("boot"); b.textContent = msg; b.classList.add("error"); }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();
