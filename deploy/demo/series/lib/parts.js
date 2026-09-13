// Reusable pieces for the video series, built on the Motion engine. Every
// helper adds a node to the scene and registers the tracks that animate it;
// positions are absolute pixels on the 1920x1080 stage.
(function () {
  'use strict';
  const { el, svg, fx, ease, clamp } = window.Motion;

  const ZYBUU = `<svg viewBox="0 0 256 256" aria-hidden="true"><defs><linearGradient id="zg" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stop-color="#5CC4FF"/><stop offset="50%" stop-color="#2A8CF0"/><stop offset="100%" stop-color="#0B3C8C"/></linearGradient><mask id="zc"><rect width="256" height="256" fill="#fff"/><g fill="#000"><rect x="70" y="62" width="126" height="34" rx="9"/><rect x="60" y="160" width="126" height="34" rx="9"/><path d="M156 96 H200 L100 160 H56 Z"/></g></mask></defs><rect x="14" y="14" width="228" height="228" rx="58" fill="url(#zg)" mask="url(#zc)"/></svg>`;
  const TITAN = `<svg viewBox="0 0 256 256" aria-hidden="true"><defs><linearGradient id="tg" x1="0" y1="0" x2="1" y2="1"><stop offset="0%" stop-color="#5CC4FF"/><stop offset="55%" stop-color="#2A8CF0"/><stop offset="100%" stop-color="#0B3C8C"/></linearGradient><radialGradient id="tc" cx="40%" cy="35%" r="70%"><stop offset="0%" stop-color="#FFFFFF"/><stop offset="70%" stop-color="#DDEFFF"/><stop offset="100%" stop-color="#9ED2FF"/></radialGradient><radialGradient id="tw" cx="50%" cy="50%" r="50%"><stop offset="0%" stop-color="#5CC4FF" stop-opacity=".5"/><stop offset="100%" stop-color="#5CC4FF" stop-opacity="0"/></radialGradient></defs><g fill="none" stroke="url(#tg)" stroke-width="28" stroke-linecap="round" stroke-linejoin="round"><path d="M104 34 H64 a20 20 0 0 0 -20 20 V202 a20 20 0 0 0 20 20 H104"/><path d="M152 34 H192 a20 20 0 0 1 20 20 V202 a20 20 0 0 1 -20 20 H152"/></g><circle cx="128" cy="128" r="60" fill="url(#tw)"/><circle cx="128" cy="128" r="31" fill="url(#tc)"/></svg>`;

  const pos = (n, x, y, w, h) => { n.style.left = x + 'px'; n.style.top = y + 'px'; if (w) n.style.width = w + 'px'; if (h) n.style.height = h + 'px'; return n; };

  const Parts = {
    marks: { zybuu: ZYBUU, titan: TITAN },

    logo(s, { x, y, text, prod, mark = 'zybuu', at = 0, size = 120 }) {
      const n = el('div', 'logo', (mark === 'titan' ? TITAN : ZYBUU) + `<b>${text}${prod ? `<span class="prod"> / ${prod}</span>` : ''}</b>`);
      n.querySelector('svg').style.width = n.querySelector('svg').style.height = size + 'px';
      n.querySelector('b').style.fontSize = Math.round(size * 0.8) + 'px';
      pos(n, x, y); s.el.appendChild(n);
      s.at(at, 0.9, fx.scaleIn(n, 0.9), ease.outBack);
      return n;
    },
    kicker(s, text, x, y, at) {
      const n = pos(el('div', 'k', text), x, y); s.el.appendChild(n); s.rise(n, at, 0.6, 12); return n;
    },
    heading(s, html, { x = 96, y = 200, w = 1300, cls = 'l', at = 0, words = true }) {
      const n = pos(el('div', 'h ' + cls), x, y, w); s.el.appendChild(n);
      if (words) {
        // words() needs plain text; keep <span class="hl"> by animating per word inside
        n.innerHTML = html;
        const spans = [];
        const walk = (node) => {
          [...node.childNodes].forEach((c) => {
            if (c.nodeType === 3) {
              const frag = document.createDocumentFragment();
              c.textContent.split(/(\s+)/).forEach((w) => {
                if (/^\s+$/.test(w) || w === '') { frag.appendChild(document.createTextNode(w)); return; }
                const sp = el('span', 'w', w); spans.push(sp); frag.appendChild(sp);
              });
              node.replaceChild(frag, c);
            } else walk(c);
          });
        };
        walk(n);
        n.style.opacity = 1;
        s.at(at, 0.9 + spans.length * 0.06, (p) => {
          const k = spans.length;
          spans.forEach((sp, i) => { const q = clamp(p * (k + 3) - i, 0, 1); sp.style.opacity = q; sp.style.transform = `translateY(${(1 - q) * 16}px)`; });
        });
      } else s.rise(n, at, 0.8, 22);
      return n;
    },
    para(s, html, { x = 96, y = 520, w = 1100, at = 0, size }) {
      const n = pos(el('div', 'p', html), x, y, w); if (size) n.style.fontSize = size + 'px';
      s.el.appendChild(n); s.rise(n, at, 0.7, 16); return n;
    },
    card(s, { x, y, w, h, icon, title, text, at, glow, html }) {
      const n = pos(el('div', 'card' + (glow ? ' glow' : '')), x, y, w, h);
      n.innerHTML = html || `${icon ? `<div class="ic">${icon}</div>` : ''}<h3>${title}</h3><p>${text || ''}</p>`;
      s.el.appendChild(n); s.at(at, 0.7, fx.rise(n, 26), ease.out); return n;
    },
    pill(s, html, { x, y, at, cls = '' }) {
      const n = pos(el('div', 'pill ' + cls, html), x, y); s.el.appendChild(n); s.at(at, 0.5, fx.scaleIn(n, 0.85), ease.outBack); return n;
    },
    fig(s, { x, y, from = 0, to, label, at, fmt }) {
      const n = pos(el('div', 'fig', `<b>0</b><span>${label}</span>`), x, y); s.el.appendChild(n);
      s.rise(n, at, 0.6, 20);
      s.at(at + 0.1, 1.4, fx.count(n.querySelector('b'), from, to, fmt), ease.outExpo);
      return n;
    },
    check(s, text, { x, y, at, no = false }) {
      const n = pos(el('div', 'check' + (no ? ' no' : ''), `<i>${no ? '!' : '✓'}</i><span>${text}</span>`), x, y);
      s.el.appendChild(n); s.rise(n, at, 0.5, 14); return n;
    },
    lower(s, text, at) {
      const n = el('div', 'lower', `<i></i><span>${text}</span>`); s.el.appendChild(n); s.rise(n, at, 0.5, 10); return n;
    },
    foot(s, text, at = 0) { const n = el('div', 'foot', text); s.el.appendChild(n); s.fade(n, at, 0.6); return n; },
    rule(s, { x, y, w, at }) { const n = pos(el('div', 'rule'), x, y, w); s.el.appendChild(n); s.at(at, 0.8, (p) => { n.style.opacity = 1; n.style.width = w * p + 'px'; }); return n; },
    callout(s, html, { x, y, at, w }) { const n = pos(el('div', 'callout', html), x, y, w); s.el.appendChild(n); s.at(at, 0.45, fx.scaleIn(n, 0.9), ease.outBack); return n; },

    // A terminal whose lines appear on their own schedule. lines: [cls, text, at, typed?]
    terminal(s, { x, y, w, h, title = '~/work — titan', at = 0, lines = [], size }) {
      const n = pos(el('div', 'term'), x, y, w, h);
      n.innerHTML = `<div class="bar"><i style="background:#FF5F57"></i><i style="background:#FEBC2E"></i><i style="background:#28C840"></i><span class="t">${title}</span></div><div class="body"></div>`;
      if (size) n.querySelector('.body').style.fontSize = size + 'px';
      s.el.appendChild(n); s.at(at, 0.7, fx.rise(n, 30));
      const body = n.querySelector('.body');
      lines.forEach(([cls, text, t, typed]) => {
        const ln = el('div', 'ln ' + (cls || '')); body.appendChild(ln);
        if (typed) { s.at(t, Math.max(0.6, text.length * 0.028), (p) => { ln.style.opacity = 1; fx.type(ln, text)(p); }, ease.linear); }
        else { ln.textContent = text || ' '; s.at(t, 0.25, fx.fade(ln)); }
      });
      return n;
    },

    // Real footage in a screen frame. src is an asset key; `from` is the
    // offset in the clip that corresponds to scene-local time `at`.
    screen(s, { x, y, w, h, src, from = 0, at, cap, speed = 1, still }) {
      const n = pos(el('div', 'screen'), x, y, w, h);
      if (still) n.innerHTML = `<img src="${ASSETS[still] || still}">`;
      else n.innerHTML = `<video muted preload="auto" src="${ASSETS[src] || src}"></video>`;
      s.el.appendChild(n); s.at(at, 0.8, fx.scaleIn(n, 0.94), ease.out);
      if (cap) {
        // Below the frame, never over the footage: a caption on top of a
        // screen hides the one part of the screen that names what it is.
        const c = pos(el('div', 'pill', cap), x, y + h + 16); c.style.fontSize = '20px'; s.el.appendChild(c);
        s.at(at + 0.4, 0.5, fx.rise(c, 10));
      }
      const v = n.querySelector('video');
      if (v) s.each((t) => { v.dataset.t = Math.max(0, from + (t - at) * speed); });
      return n;
    },

    // Nodes and edges. nodes: {id,x,y,w,h,html,cls,at}; edges: {from,to,at,cls,pulse}
    diagram(s, { nodes, edges = [], pulses = true }) {
      const g = svg('svg', { class: 'diagram', width: 1920, height: 1080 }); g.style.left = 0; g.style.top = 0;
      s.el.appendChild(g);
      const byId = {};
      nodes.forEach((d) => {
        const n = pos(el('div', 'node ' + (d.cls || ''), d.html), d.x, d.y, d.w, d.h); s.el.appendChild(n);
        byId[d.id] = d; s.at(d.at, 0.6, fx.scaleIn(n, 0.85), ease.outBack); d.el = n;
      });
      const c = (d) => ({ x: d.x + (d.w || 200) / 2, y: d.y + (d.h || 90) / 2 });
      edges.forEach((e) => {
        const a = c(byId[e.from]), b = c(byId[e.to]);
        const mx = (a.x + b.x) / 2;
        const dPath = e.curve === false ? `M${a.x},${a.y} L${b.x},${b.y}` : `M${a.x},${a.y} C${mx},${a.y} ${mx},${b.y} ${b.x},${b.y}`;
        const path = svg('path', { d: dPath, class: 'edge ' + (e.cls || '') }); g.appendChild(path);
        s.at(e.at, 0.8, fx.draw(path));
        if (pulses && e.pulse !== false) {
          const dot = svg('circle', { r: 6, fill: e.cls === 'bad' ? '#E08A4C' : '#6FD0FF' }); dot.style.filter = 'drop-shadow(0 0 8px #6FD0FF)'; g.appendChild(dot);
          const mv = fx.along(dot, path);
          s.each((t) => { const k = (t - e.at - 0.8) / 1.6; mv(k < 0 ? 0 : (k % 1)); if (k < 0) dot.style.opacity = 0; });
        }
      });
      return byId;
    },
  };

  const ICONS = {
    building: '<svg viewBox="0 0 24 24"><path d="M3 21h18M5 21V7l7-4 7 4v14M9 21v-6h6v6"/></svg>',
    layers: '<svg viewBox="0 0 24 24"><path d="M12 3l8 4.5v9L12 21l-8-4.5v-9L12 3zM12 12l8-4.5M12 12v9M12 12L4 7.5"/></svg>',
    shield: '<svg viewBox="0 0 24 24"><path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6l8-3z"/><path d="M9 12l2 2 4-4"/></svg>',
    record: '<svg viewBox="0 0 24 24"><path d="M4 5h16v14H4zM8 9h8M8 13h5"/></svg>',
    code: '<svg viewBox="0 0 24 24"><path d="M8 8l-4 4 4 4M16 8l4 4-4 4M14 4l-4 16"/></svg>',
    model: '<svg viewBox="0 0 24 24"><path d="M4 6h16M4 12h16M4 18h10"/><circle cx="19" cy="18" r="2"/></svg>',
    cloud: '<svg viewBox="0 0 24 24"><path d="M7 18a4 4 0 0 1-.5-8A6 6 0 0 1 18 9a4 4 0 0 1 0 9H7z"/></svg>',
    lock: '<svg viewBox="0 0 24 24"><rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></svg>',
    gauge: '<svg viewBox="0 0 24 24"><path d="M4 14a8 8 0 0 1 16 0M12 14l4-4"/><circle cx="12" cy="14" r="1.5"/></svg>',
    eye: '<svg viewBox="0 0 24 24"><path d="M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z"/><circle cx="12" cy="12" r="3"/></svg>',
    gpu: '<svg viewBox="0 0 24 24"><rect x="3" y="6" width="18" height="12" rx="2"/><path d="M7 10h4v4H7zM14 10h3M14 14h3"/></svg>',
    branch: '<svg viewBox="0 0 24 24"><circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="9" r="2"/><path d="M6 7v10M18 11c0 4-12 2-12 6"/></svg>',
    clock: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
    plug: '<svg viewBox="0 0 24 24"><path d="M9 3v5M15 3v5M6 8h12v4a6 6 0 0 1-12 0V8zM12 18v3"/></svg>',
    user: '<svg viewBox="0 0 24 24"><circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/></svg>',
    box: '<svg viewBox="0 0 24 24"><path d="M3 7l9-4 9 4v10l-9 4-9-4V7zM3 7l9 4 9-4M12 11v10"/></svg>',
  };
  window.Parts = Parts; window.ICONS = ICONS;
})();
