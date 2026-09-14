// Video 1 — Zybuu: who we are and why. An announcement, 2½ minutes.
window.VIDEOS['01-zybuu'] = () => {
  const { fx, ease } = Motion;
  return [
    // ---- b1: the mark, then the line ---------------------------------
    { beat: 'b1', build(s) {
      const logo = Parts.logo(s, { x: 0, y: 0, text: 'Zybuu', at: 0.2, size: 150 });
      logo.style.left = '50%'; logo.style.top = '38%'; logo.style.transform = 'translate(-50%,-50%)';
      s.at(0.2, 1.0, (p) => { logo.style.opacity = p; logo.style.transform = `translate(-50%,-50%) scale(${0.9 + 0.1 * ease.outBack(p)})`; });
      Parts.rule(s, { x: 760, y: 610, w: 400, at: 1.4 });
      const tag = Parts.para(s, 'Infrastructure for AI that has to stay <b>under your control.</b>', { x: 360, y: 650, w: 1200, at: 1.9, size: 44 });
      tag.style.textAlign = 'center';
      Parts.foot(s, 'ZYBUU · SEPTEMBER 2026', 2.6);
    } },

    // ---- b2: the moment --------------------------------------------------
    { beat: 'b2', build(s) {
      Parts.kicker(s, 'The moment', 96, 120, 0.1);
      Parts.heading(s, 'Every enterprise is about to <span class="hl">run agents.</span>', { x: 96, y: 170, w: 900, cls: 'l', at: 0.3 });
      Parts.para(s, 'Software that reads, decides, acts and reports — across everything the business runs on.', { x: 96, y: 520, w: 800, at: 3.2, size: 32 });
      const b = 4.6; // "the code, the tickets, the records, the systems"
      Parts.diagram(s, {
        nodes: [
          { id: 'agent', x: 1280, y: 440, w: 300, h: 110, html: 'Agent<small>reads · decides · acts · reports</small>', cls: 'hot', at: 1.6 },
          { id: 'code', x: 1010, y: 200, w: 220, h: 90, html: 'Code', at: b + 0.0 },
          { id: 'tick', x: 1600, y: 200, w: 220, h: 90, html: 'Tickets', at: b + 0.45 },
          { id: 'rec', x: 1010, y: 720, w: 220, h: 90, html: 'Records', at: b + 0.9 },
          { id: 'sys', x: 1600, y: 720, w: 220, h: 90, html: 'Systems', at: b + 1.35 },
        ],
        edges: [
          { from: 'code', to: 'agent', at: b + 0.2 }, { from: 'tick', to: 'agent', at: b + 0.65 },
          { from: 'agent', to: 'rec', at: b + 1.1 }, { from: 'agent', to: 'sys', at: b + 1.55 },
        ],
      });
    } },

    // ---- b3: how it is sold ---------------------------------------------
    { beat: 'b3', build(s) {
      Parts.kicker(s, 'How it is sold today', 96, 120, 0.1);
      Parts.heading(s, 'One way.', { x: 96, y: 170, w: 900, cls: 'l', at: 0.3 });
      Parts.diagram(s, {
        nodes: [
          { id: 'you', x: 120, y: 520, w: 380, h: 120, html: 'Your data<small>code · records · customers</small>', at: 0.8 },
          { id: 'cloud', x: 770, y: 520, w: 380, h: 120, html: "A vendor's cloud", cls: 'bad', at: 1.9 },
          { id: 'model', x: 1420, y: 520, w: 380, h: 120, html: "A vendor's model", cls: 'bad', at: 3.0 },
        ],
        edges: [
          { from: 'you', to: 'cloud', at: 4.2, cls: 'bad', curve: false },
          { from: 'cloud', to: 'model', at: 4.6, cls: 'bad', curve: false },
        ],
      });
      Parts.callout(s, 'your data leaves the building — <b>on every turn</b>', { x: 560, y: 720, at: 4.8 });
      Parts.check(s, 'Nothing you can audit', { x: 120, y: 860, at: 6.6, no: true });
      Parts.check(s, 'Nothing you can keep', { x: 700, y: 860, at: 7.1, no: true });
      Parts.check(s, 'Nothing you can prove', { x: 1240, y: 860, at: 7.6, no: true });
    } },

    // ---- b4: the wall ----------------------------------------------------
    { beat: 'b4', build(s) {
      Parts.kicker(s, 'Who cannot buy it that way', 96, 120, 0.1);
      const names = ['Banks', 'Insurers', 'Hospitals', 'Defence', 'Government', 'Data-residency clauses'];
      names.forEach((n, i) => Parts.pill(s, n, { x: 96 + (i % 3) * 420, y: 200 + Math.floor(i / 3) * 90, at: 0.5 + i * 0.55 }));
      Parts.heading(s, 'That is not a product.<br>It is a <span class="hl">wall.</span>', { x: 96, y: 520, w: 1100, cls: 'l', at: 6.6 });
      // a wall that rises on the right
      const wall = Motion.el('div'); wall.style.cssText = 'position:absolute;left:1380px;bottom:0;width:440px;height:0;background:linear-gradient(180deg,rgba(224,138,76,.0),rgba(224,138,76,.28));border-top:3px solid #E08A4C;border-radius:8px 8px 0 0';
      s.el.appendChild(wall); s.at(7.0, 1.2, (p) => { wall.style.height = 760 * p + 'px'; }, ease.outExpo);
    } },

    // ---- b5: building it themselves -------------------------------------
    { beat: 'b5', build(s) {
      Parts.kicker(s, 'So they build it themselves', 96, 120, 0.1);
      Parts.heading(s, 'Most of it is the same thing, <span class="hl">badly.</span>', { x: 96, y: 170, w: 1300, cls: 'm', at: 0.3 });
      const items = [
        ['A loop', 'The easy part, and the only part most stop at.', 3.6],
        ['A sandbox that is not quite one', 'A boundary the agent can talk its way around.', 4.9],
        ['A log that is not quite complete', 'Enough to describe a run. Not enough to replay it.', 6.5],
        ['An approval that needs a witness', 'Works when someone is watching. Nobody always is.', 8.4],
      ];
      items.forEach(([t, d, at], i) => {
        const c = Parts.card(s, { x: 96 + (i % 2) * 880, y: 400 + Math.floor(i / 2) * 250, w: 840, h: 210, title: t, text: d, at });
        c.style.borderColor = 'rgba(224,138,76,.5)';
      });
    } },

    // ---- b6: Zybuu builds it ----------------------------------------------
    { beat: 'b6', build(s) {
      Parts.logo(s, { x: 96, y: 96, text: 'Zybuu', at: 0.1, size: 72 });
      Parts.heading(s, 'We build that layer. <span class="hl">Properly. Once.</span>', { x: 96, y: 230, w: 1300, cls: 'l', at: 0.6 });
      const tiers = [['Software as a service', 'hosted by Zybuu', ICONS.cloud, 5.3], ['Private cloud', 'in your own cloud or data centre', ICONS.building, 6.2], ['Fully air-gapped', 'no route to the internet at all', ICONS.lock, 7.0]];
      tiers.forEach(([t, d, ic, at], i) => Parts.card(s, { x: 96 + i * 590, y: 520, w: 560, h: 270, icon: ic, title: t, text: d, at }));
      Parts.rule(s, { x: 96, y: 840, w: 1730, at: 8.6 });
      Parts.para(s, 'The same guarantees at every tier.', { x: 96, y: 870, w: 1200, at: 8.8, size: 36 });
    } },

    // ---- b7: the vision --------------------------------------------------
    { beat: 'b7', build(s) {
      Parts.kicker(s, 'Our vision', 96, 120, 0.1);
      Parts.heading(s, 'The layer <span class="hl">underneath</span> the agent.', { x: 96, y: 170, w: 1200, cls: 'l', at: 0.4 });
      const verbs = [['Decide', 'what a model may do', 2.6], ['Do it safely', 'inside a boundary it cannot lift', 3.6], ['Record', 'every action, every decision', 4.6], ['Prove', 'afterwards, exactly what happened', 5.4]];
      verbs.forEach(([v, d, at], i) => {
        const c = Parts.card(s, { x: 96 + i * 440, y: 500, w: 410, h: 230, title: v, text: d, at });
        c.querySelector('h3').classList.add('hl');
      });
      Parts.para(s, 'As a <b>product</b>, not a project.', { x: 96, y: 820, w: 1200, at: 7.4, size: 40 });
    } },

    // ---- b8: Abhed -------------------------------------------------------
    { beat: 'b8', build(s) {
      Parts.kicker(s, 'Our first product', 96, 120, 0.1);
      const logo = Parts.logo(s, { x: 96, y: 200, text: 'Abhed', mark: 'abhed', at: 1.0, size: 150 });
      Parts.heading(s, 'The agent harness for work that <span class="hl">cannot leave the building.</span>', { x: 96, y: 420, w: 1400, cls: 'm', at: 2.4 });
      Parts.pill(s, '<b>●</b> Live today', { x: 96, y: 760, at: 6.2, cls: 'ok' });
      Parts.pill(s, 'invite-only', { x: 340, y: 760, at: 6.5 });
      Parts.pill(s, 'abhed.zybuu.com', { x: 570, y: 760, at: 6.8 });
      Parts.para(s, 'And only the beginning.', { x: 96, y: 880, w: 900, at: 8.0, size: 34 });
    } },

    // ---- b9: the platform ------------------------------------------------
    { beat: 'b9', build(s) {
      Parts.kicker(s, 'On the same foundations', 96, 120, 0.1);
      Parts.heading(s, 'One product shipped. A platform underneath it.', { x: 96, y: 170, w: 1300, cls: 'm', at: 0.3 });
      const items = [['Workload benchmarking', ICONS.gauge, 2.6], ['Agent security & red team', ICONS.shield, 3.6], ['Observability for agents', ICONS.eye, 4.5], ['Inference infrastructure', ICONS.gpu, 5.5], ['Private cloud deployment', ICONS.box, 6.6]];
      items.forEach(([t, ic, at], i) => {
        const c = Parts.card(s, { x: 96 + (i % 3) * 590, y: 400 + Math.floor(i / 3) * 250, w: 560, h: 210, icon: ic, title: t, text: '<span class="mono muted" style="font-size:20px;letter-spacing:.08em">PLANNED</span>', at });
      });
      Parts.card(s, { x: 1276, y: 650, w: 560, h: 210, icon: ICONS.layers, title: 'Abhed', text: '<span class="mono" style="font-size:20px;letter-spacing:.08em;color:var(--ok)">LIVE</span>', at: 1.2, glow: true });
    } },

    // ---- b10: close ---------------------------------------------------------
    { beat: 'b10', build(s) {
      const logo = Parts.logo(s, { x: 0, y: 0, text: 'Zybuu', at: 0.2, size: 150 });
      logo.style.left = '50%'; logo.style.top = '36%'; logo.style.transform = 'translate(-50%,-50%)';
      s.at(0.2, 1.0, (p) => { logo.style.opacity = p; logo.style.transform = `translate(-50%,-50%) scale(${0.9 + 0.1 * ease.outBack(p)})`; });
      const h = Parts.heading(s, 'Run AI where your data <span class="hl">already lives.</span>', { x: 260, y: 560, w: 1400, cls: 'm', at: 1.2 });
      h.style.textAlign = 'center';
      const u = Parts.pill(s, 'zybuu.com', { x: 840, y: 800, at: 4.0 }); u.style.fontSize = '30px';
    } },
  ];
};
