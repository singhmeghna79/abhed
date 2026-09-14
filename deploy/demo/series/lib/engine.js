// A small, deterministic motion engine for the Abhed video series.
//
// Everything on screen is a pure function of time. The renderer sets a global
// clock with __seek(t) and takes a screenshot; nothing runs on the wall clock,
// so a frame rendered twice is the same frame, and the picture cannot drift
// from the narration: the timeline that positions every scene is derived from
// the recorded audio, beat by beat, before a single frame is drawn.
//
// A video is a list of scenes. A scene starts on a named beat of the narration
// and lasts until the next scene's beat. Inside a scene, everything is
// positioned relative to beats — "reveal this bullet 0.4s after beat 3 starts"
// — so re-recording the voice re-times the picture for free.

(function () {
  'use strict';

  // ---- easing ------------------------------------------------------------
  const clamp = (x, a = 0, b = 1) => Math.max(a, Math.min(b, x));
  const ease = {
    linear: (x) => x,
    out: (x) => 1 - Math.pow(1 - x, 3),
    in: (x) => x * x * x,
    inOut: (x) => (x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2),
    outBack: (x) => { const c = 1.70158; return 1 + (c + 1) * Math.pow(x - 1, 3) + c * Math.pow(x - 1, 2); },
    outExpo: (x) => (x >= 1 ? 1 : 1 - Math.pow(2, -10 * x)),
  };
  // progress of an animation starting at `start` lasting `dur`, at time t
  const P = (t, start, dur, e = ease.out) => e(clamp((t - start) / dur));

  // ---- DOM helpers -------------------------------------------------------
  function el(tag, cls, html) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (html !== undefined) n.innerHTML = html;
    return n;
  }
  const svgNS = 'http://www.w3.org/2000/svg';
  function svg(tag, attrs = {}) {
    const n = document.createElementNS(svgNS, tag);
    for (const k in attrs) n.setAttribute(k, attrs[k]);
    return n;
  }

  // ---- animated primitives ----------------------------------------------
  // Each returns a function apply(p) with p in [0,1].
  const fx = {
    // fade + rise
    rise(node, dy = 18) {
      return (p) => { node.style.opacity = p; node.style.transform = `translateY(${(1 - p) * dy}px)`; };
    },
    fade(node) { return (p) => { node.style.opacity = p; }; },
    scaleIn(node, from = 0.92) {
      return (p) => { node.style.opacity = p; node.style.transform = `scale(${from + (1 - from) * p})`; };
    },
    // wipe a text node in, word by word
    words(node, text) {
      const ws = text.split(/(\s+)/);
      node.innerHTML = ws.map((w) => (/^\s+$/.test(w) ? w : `<span class="w">${w}</span>`)).join('');
      const spans = [...node.querySelectorAll('.w')];
      return (p) => {
        const n = spans.length;
        spans.forEach((s, i) => {
          const q = clamp(p * (n + 2) - i, 0, 1);
          s.style.opacity = q; s.style.transform = `translateY(${(1 - q) * 10}px)`;
        });
      };
    },
    // typewriter with a caret
    type(node, text) {
      return (p) => {
        const n = Math.floor(text.length * p);
        node.textContent = text.slice(0, n);
        node.classList.toggle('caret', p < 1);
      };
    },
    // counts a number up, formatted
    count(node, from, to, fmt = (v) => Math.round(v).toLocaleString()) {
      return (p) => { node.textContent = fmt(from + (to - from) * p); };
    },
    // draws an SVG path (stroke-dash)
    draw(path) {
      const len = path.getTotalLength ? path.getTotalLength() : 1000;
      path.style.strokeDasharray = len; path.style.strokeDashoffset = len;
      return (p) => { path.style.strokeDashoffset = len * (1 - p); };
    },
    // moves a dot along a path
    along(dot, path) {
      const len = path.getTotalLength();
      return (p) => {
        const pt = path.getPointAtLength(len * p);
        dot.setAttribute('cx', pt.x); dot.setAttribute('cy', pt.y);
        dot.style.opacity = p > 0 && p < 1 ? 1 : 0;
      };
    },
    // grows a bar (height) to a fraction of its container
    bar(node, frac) { return (p) => { node.style.height = `${frac * p * 100}%`; }; },
  };

  // ---- scene runtime ------------------------------------------------------
  // A scene builds its DOM once and registers "tracks": (start, dur, apply,
  // easing) relative to the scene's local time, where start may reference a
  // beat via s.beat('b3') + offset.
  class Scene {
    constructor(def, root, timeline) {
      this.def = def; this.root = root; this.timeline = timeline;
      this.tracks = []; this.updaters = [];
      this.start = timeline.beatStart(def.beat);
      this.el = el('section', 'scene ' + (def.cls || ''));
      root.appendChild(this.el);
      def.build(this, this.el);
    }
    // seconds (scene-local) at which beat `id` starts
    beat(id) { return this.timeline.beatStart(id) - this.start; }
    beatDur(id) { return this.timeline.beatDur(id); }
    at(start, dur, apply, e = ease.out) { this.tracks.push({ start, dur, apply, e }); return this; }
    // convenience: rise-in `node` at scene-local time
    rise(node, start, dur = 0.7, dy) { return this.at(start, dur, fx.rise(node, dy)); }
    fade(node, start, dur = 0.6) { return this.at(start, dur, fx.fade(node)); }
    // a continuous updater (t local) for things like drifting glows
    each(fn) { this.updaters.push(fn); return this; }
    update(tLocal) {
      for (const tr of this.tracks) tr.apply(P(tLocal, tr.start, tr.dur, tr.e));
      for (const u of this.updaters) u(tLocal);
    }
  }

  class Timeline {
    constructor(beats) { this.beats = beats; this.index = new Map(beats.map((b) => [b.id, b])); }
    beatStart(id) { const b = this.index.get(id); if (!b) throw new Error('no beat ' + id); return b.start; }
    beatDur(id) { return this.index.get(id).dur; }
    get total() { const l = this.beats[this.beats.length - 1]; return l.start + l.dur; }
  }

  const XFADE = 0.6;
  class Video {
    constructor(defs, beats, root) {
      this.timeline = new Timeline(beats);
      this.root = root;
      this.scenes = defs.map((d) => new Scene(d, root, this.timeline));
      this.scenes.forEach((s, i) => { s.end = i + 1 < this.scenes.length ? this.scenes[i + 1].start : this.timeline.total + 1.5; });
      this.duration = this.timeline.total + 1.5;
    }
    seek(t) {
      for (const s of this.scenes) {
        const on = t >= s.start - XFADE && t < s.end + XFADE;
        s.el.style.display = on ? '' : 'none';
        if (!on) continue;
        // cross-fade at both ends
        const fin = clamp((t - s.start) / XFADE), fout = clamp((s.end - t) / XFADE);
        s.el.style.opacity = Math.min(fin, fout);
        s.update(t - s.start);
      }
    }
  }

  window.Motion = { Video, Scene, ease, P, clamp, el, svg, fx, XFADE };
})();
