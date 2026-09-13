// Video 2 — Titan: what it is and what it can do. About 3½ minutes.
window.VIDEOS['02-titan'] = () => {
  const { fx, ease, el } = Motion;
  const feature = (s, { kicker, title, cards, at = 0.1, cardAt = 2.4, cols = 3 }) => {
    Parts.kicker(s, kicker, 96, 120, at);
    Parts.heading(s, title, { x: 96, y: 170, w: 1400, cls: 'l', at: at + 0.2 });
    const w = cols === 3 ? 560 : 840, gap = 30;
    cards.forEach((c, i) => Parts.card(s, { x: 96 + (i % cols) * (w + gap), y: 520 + Math.floor(i / cols) * 250, w, h: 230, icon: c.icon, title: c.title, text: c.text, at: cardAt + i * 0.7, glow: c.glow }));
  };
  return [
    { beat: 'b1', build(s) {
      const logo = Parts.logo(s, { x: 0, y: 0, text: 'Titan', mark: 'titan', at: 0.2, size: 160 });
      logo.style.left = '50%'; logo.style.top = '40%'; logo.style.transform = 'translate(-50%,-50%)';
      s.at(0.2, 1.0, (p) => { logo.style.opacity = p; logo.style.transform = `translate(-50%,-50%) scale(${0.9 + 0.1 * ease.outBack(p)})`; });
      const t = Parts.para(s, "Zybuu's first product · <b>a deep agent harness</b>", { x: 360, y: 640, w: 1200, at: 1.6, size: 40 }); t.style.textAlign = 'center';
      Parts.foot(s, 'ZYBUU · TITAN · SERIES 2 OF 4', 2.2);
    } },

    // b2: what a harness is — the model in the middle, the harness rings around it
    { beat: 'b2', build(s) {
      Parts.kicker(s, 'What a harness is', 96, 120, 0.1);
      Parts.heading(s, 'Everything <span class="hl">around</span> the model.', { x: 96, y: 170, w: 900, cls: 'l', at: 0.3 });
      const cx = 1380, cy = 600;
      const model = el('div', 'node hot', 'Model<small>replaceable</small>'); model.style.cssText += `left:${cx - 130}px;top:${cy - 55}px;width:260px;height:110px`; s.el.appendChild(model);
      s.at(0.6, 0.7, fx.scaleIn(model, 0.8), ease.outBack);
      const ring = ['The loop', 'Tool contracts', 'Permission engine', 'Sandbox', 'The record'];
      ring.forEach((label, i) => {
        const a = -Math.PI / 2 + i * (2 * Math.PI / ring.length), r = 330;
        const n = el('div', 'node', label); n.style.cssText += `left:${cx + Math.cos(a) * r - 120}px;top:${cy + Math.sin(a) * r - 45}px;width:240px;height:90px`;
        s.el.appendChild(n); s.at(2.5 + i * 1.05, 0.6, fx.scaleIn(n, 0.8), ease.outBack);
      });
      // an orbit ring drawn behind
      const g = Motion.svg('svg', { class: 'diagram', width: 1920, height: 1080 }); g.style.left = 0; g.style.top = 0;
      const c = Motion.svg('circle', { cx, cy, r: 330, fill: 'none', stroke: 'rgba(59,169,255,.35)', 'stroke-width': 2, 'stroke-dasharray': '6 10' }); g.appendChild(c); s.el.insertBefore(g, model);
      s.at(2.2, 1.2, (p) => { c.style.opacity = p; });
      Parts.para(s, 'Change the harness and the same model becomes a <b>different product.</b>', { x: 96, y: 560, w: 760, at: 8.6, size: 34 });
    } },

    { beat: 'b3', build(s) {
      feature(s, { kicker: 'Runs where the data is', title: 'One static binary. <span class="hl">Anywhere.</span>', cardAt: 2.8, cards: [
        { icon: ICONS.user, title: 'A laptop', text: 'Build it, point it at a model, run.' },
        { icon: ICONS.building, title: 'A rack in your data centre', text: 'Container, Postgres, your identity provider.' },
        { icon: ICONS.lock, title: 'An air-gapped enclave', text: 'A signed offline bundle; verify before install.' },
      ] });
    } },

    { beat: 'b4', build(s) {
      Parts.kicker(s, 'Runs any model', 96, 120, 0.1);
      Parts.heading(s, 'Twenty providers. <span class="hl">Three wire formats.</span>', { x: 96, y: 170, w: 1300, cls: 'l', at: 0.3 });
      // The twenty names in internal/model's registry, as registered.
      const provs = ['Anthropic', 'OpenAI', 'Azure OpenAI', 'Bedrock · Anthropic', 'Vertex · Anthropic', 'Vertex · Gemini', 'Gemini', 'Mistral', 'DeepSeek', 'Groq', 'Together', 'OpenRouter', 'xAI', 'watsonx', 'Ollama', 'vLLM', 'SGLang', 'TGI', 'llama.cpp', 'OpenAI-compatible'];
      provs.forEach((p, i) => Parts.pill(s, p, { x: 96 + (i % 6) * 300, y: 500 + Math.floor(i / 6) * 76, at: 2.0 + i * 0.16 }));
      Parts.card(s, { x: 96, y: 830, w: 1000, h: 130, at: 8.8, glow: true, html: '<p style="font-size:28px"><b style="color:var(--ink)">including the one on your own GPUs.</b> Changing vendors is a line of config — not a migration.</p>' });
    } },

    { beat: 'b5', build(s) {
      Parts.kicker(s, 'Sandboxed by default', 96, 120, 0.1);
      Parts.heading(s, 'A boundary the agent <span class="hl">cannot lift.</span>', { x: 96, y: 170, w: 1200, cls: 'l', at: 0.3 });
      // boundary box with the agent inside and blocked arrows out
      const box = el('div'); box.style.cssText = 'position:absolute;left:1180px;top:420px;width:640px;height:440px;border:2px dashed rgba(59,169,255,.7);border-radius:30px;opacity:0';
      s.el.appendChild(box); s.at(1.2, 0.8, fx.fade(box));
      const lab = el('div', 'pill', 'sandbox · process | container | vm'); lab.style.cssText += 'left:1200px;top:396px'; s.el.appendChild(lab); s.at(1.6, 0.5, fx.scaleIn(lab, 0.9));
      Parts.diagram(s, { nodes: [
        { id: 'a', x: 1350, y: 560, w: 300, h: 110, html: 'Agent<small>reads · writes · runs</small>', cls: 'hot', at: 2.0 },
        { id: 'fs', x: 860, y: 560, w: 240, h: 90, html: 'Host filesystem', cls: 'bad', at: 3.4 },
        { id: 'net', x: 1350, y: 930, w: 300, h: 90, html: 'The internet', cls: 'bad', at: 4.2 },
      ], edges: [{ from: 'a', to: 'fs', at: 3.6, cls: 'bad', curve: false, pulse: false }, { from: 'a', to: 'net', at: 4.4, cls: 'bad', curve: false, pulse: false }] });
      Parts.check(s, 'Deny rules are absolute', { x: 96, y: 520, at: 7.2 });
      Parts.check(s, 'Hold in every mode, including bypass', { x: 96, y: 600, at: 8.2 });
      Parts.check(s, 'No plugin, profile or mode can widen them', { x: 96, y: 680, at: 9.4 });
    } },

    { beat: 'b6', build(s) {
      Parts.kicker(s, 'Writes are a permission', 96, 120, 0.1);
      Parts.heading(s, 'A change <span class="hl">waits for a person.</span>', { x: 96, y: 170, w: 1200, cls: 'l', at: 0.3 });
      // an approval card, like the console's
      const card = el('div', 'card'); card.style.cssText += 'left:1060px;top:440px;width:760px;height:330px;border-color:rgba(224,138,76,.6)';
      card.innerHTML = `<div class="mono" style="font-size:20px;color:var(--warn);letter-spacing:.1em">APPROVAL NEEDED</div>
        <div style="font-size:30px;margin:14px 0 10px;font-weight:650">write <span class="mono" style="font-weight:500;color:var(--ink-2)">/workspace/NOTES.md</span></div>
        <div class="mono" style="font-size:22px;color:var(--ink-2);background:#0B0E14;border-radius:12px;padding:14px 18px">+ Titan — the agent harness for work that cannot leave the building.</div>
        <div style="display:flex;gap:14px;margin-top:22px"><span id="yes" style="background:var(--live);color:#04121F;padding:12px 26px;border-radius:12px;font-weight:700;font-size:24px">Approve</span><span style="border:1px solid var(--line-2);padding:12px 26px;border-radius:12px;font-size:24px">Deny</span></div>`;
      s.el.appendChild(card); s.at(1.4, 0.7, fx.rise(card, 26));
      const yes = card.querySelector('#yes');
      s.at(5.2, 0.3, (p) => { yes.style.boxShadow = `0 0 ${40 * p}px rgba(59,169,255,${0.7 * p})`; yes.style.transform = `scale(${1 + 0.06 * p})`; });
      const stamp = Parts.pill(s, 'approved by <b>y.singh</b> · 09:41:07 · recorded', { x: 1060, y: 800, at: 5.8, cls: 'ok' });
      Parts.para(s, 'The decision is <b>recorded</b>, with who made it.', { x: 96, y: 520, w: 800, at: 6.0, size: 34 });
      Parts.para(s, 'Nobody there to ask? The answer is <b>no</b> — never assume yes.', { x: 96, y: 640, w: 800, at: 9.4, size: 34 });
    } },

    { beat: 'b7', build(s) {
      Parts.kicker(s, 'On the record', 96, 120, 0.1);
      Parts.heading(s, 'An append-only log, <span class="hl">replayable.</span>', { x: 96, y: 170, w: 1300, cls: 'l', at: 0.3 });
      Parts.terminal(s, { x: 96, y: 470, w: 1100, h: 460, title: 'session s-mrtwxf — event log', at: 1.6, size: 22, lines: [
        ['dim', '#1  session.started    user=y.singh  mode=default', 2.2],
        ['dim', '#2  user.message       "Add a table-driven test for FToC"', 2.5],
        ['tool', '#3  tool.call          glob  {"pattern":"tempconv*"}', 2.8],
        ['res', '#4  tool.result        1 file · trust=untrusted', 3.1],
        ['tool', '#5  tool.call          write tempconv_test.go', 3.4],
        ['warn', '#6  approval.asked     write · waiting', 3.7],
        ['ok', '#7  approval.decided   approved by y.singh', 4.0],
        ['res', '#8  tool.result        Created tempconv_test.go (629 bytes)', 4.3],
        ['ok', '#9  session.ended      completed · 9 turns · 30,193 in / 1,825 out', 4.6],
      ] });
      Parts.check(s, 'Cannot be updated or deleted — by database trigger', { x: 1240, y: 520, at: 5.6 });
      Parts.check(s, 'Replay any session, step by step', { x: 1240, y: 610, at: 6.6 });
      Parts.check(s, 'Export as OpenTelemetry traces', { x: 1240, y: 700, at: 8.4 });
    } },

    { beat: 'b8', build(s) {
      Parts.kicker(s, 'Embeds without weakening', 96, 120, 0.1);
      Parts.heading(s, 'Your software, <span class="hl">the same guarantees.</span>', { x: 96, y: 170, w: 1300, cls: 'l', at: 0.3 });
      Parts.terminal(s, { x: 96, y: 460, w: 1160, h: 520, title: 'main.go', at: 1.6, size: 23, lines: [
        ['cmd', 'a, _ := titan.New(ctx, titan.Options{', 2.0],
        ['dim', '    Workspace: "/srv/work",', 2.2],
        ['dim', '    Provider:  &titan.Provider{Type: "openai-compatible", Model: "Qwen/Qwen3-32B"},', 2.4],
        ['dim', '    Deny:      []string{"bash(rm -rf *)"},', 2.6],
        ['dim', '    Approve:   askYourUser,   // no approver means refuse', 2.8],
        ['dim', '})', 3.0],
        ['cmd', '', 3.2],
        ['cmd', 'var out Review', 6.4],
        ['tool', 'err := a.RunJSON(ctx, "Review pkg/auth for injection risks.", schema, &out)', 6.7],
        ['ok', '// validated against the schema inside the loop — never invalid JSON', 8.2],
      ] });
      Parts.check(s, 'Policy, audit and deny rules hold', { x: 1300, y: 520, at: 4.0 });
      Parts.check(s, 'Structured output, validated', { x: 1300, y: 610, at: 7.6 });
      Parts.check(s, 'Steer, fork, replay, export', { x: 1300, y: 700, at: 9.0 });
    } },

    { beat: 'b9', build(s) {
      Parts.kicker(s, 'Parallel, isolated, scheduled', 96, 120, 0.1);
      Parts.heading(s, 'Subagents in <span class="hl">worktrees.</span> Runs on a <span class="hl">clock.</span>', { x: 96, y: 170, w: 1400, cls: 'l', at: 0.3 });
      Parts.diagram(s, { nodes: [
        { id: 'p', x: 120, y: 560, w: 300, h: 110, html: 'Parent agent<small>tasks · isolation: worktree</small>', cls: 'hot', at: 1.6 },
        { id: 'w1', x: 620, y: 440, w: 320, h: 90, html: 'worktree · titan/k3f9q2', at: 2.6 },
        { id: 'w2', x: 620, y: 570, w: 320, h: 90, html: 'worktree · titan/p8m2xa', at: 3.0 },
        { id: 'w3', x: 620, y: 700, w: 320, h: 90, html: 'worktree · titan/c1v7dd', at: 3.4 },
      ], edges: [{ from: 'p', to: 'w1', at: 2.6 }, { from: 'p', to: 'w2', at: 3.0 }, { from: 'p', to: 'w3', at: 3.4 }] });
      Parts.check(s, 'Two writers can never collide', { x: 120, y: 860, at: 5.2 });
      Parts.terminal(s, { x: 1120, y: 460, w: 700, h: 300, title: 'config.json', at: 6.8, size: 22, lines: [
        ['dim', '"schedules": [{', 7.0], ['cmd', '  "name":   "staging-health",', 7.2], ['cmd', '  "cron":   "@hourly",', 7.4],
        ['cmd', '  "prompt": "List pods that restarted…",', 7.6], ['cmd', '  "mode":   "auto"', 7.8], ['dim', '}]', 8.0],
      ] });
      Parts.check(s, 'Every firing is a session on the record', { x: 1120, y: 800, at: 8.8 });
    } },

    { beat: 'b10', build(s) {
      feature(s, { kicker: 'Extend it without forking it', title: 'Primitives, <span class="hl">not features.</span>', cardAt: 2.2, cols: 3, cards: [
        { icon: ICONS.plug, title: 'Extensions', text: 'Any language, over JSONL. They may veto — never permit.' },
        { icon: ICONS.layers, title: 'Skills', text: 'A directory with a procedure in it. Fetched when it applies.' },
        { icon: ICONS.box, title: 'MCP servers', text: 'Registered, disabled until enabled, credentials from the environment.' },
      ] });
      Parts.pill(s, 'custom providers from config — no rebuild', { x: 96, y: 800, at: 6.4 });
    } },

    { beat: 'b11', build(s) {
      Parts.kicker(s, 'Five surfaces', 96, 120, 0.1);
      Parts.heading(s, 'Five ways to run it. <span class="hl">One set of rules.</span>', { x: 96, y: 170, w: 1400, cls: 'l', at: 0.3 });
      const rows = [['titan', 'interactive — line editor, slash commands, steer mid-run'], ['titan -p "…"', 'headless — one prompt, typed exit code'], ['-output-format json', 'one event per line, streamed'], ['titan rpc', 'JSONL over stdio, from any language'], ['titan serve', 'REST, SSE, a web console — multi-user, tenant-scoped']];
      rows.forEach(([c, d], i) => {
        const n = Parts.card(s, { x: 96, y: 470 + i * 104, w: 1500, h: 88, at: 2.6 + i * 0.75, html: `<div style="display:flex;align-items:center;gap:30px"><span class="mono" style="font-size:26px;color:var(--live);min-width:380px">${c}</span><span style="font-size:26px;color:var(--ink-2)">${d}</span></div>` });
        n.style.padding = '24px 34px';
      });
    } },

    { beat: 'b12', build(s) {
      Parts.kicker(s, 'What is not true yet', 96, 120, 0.1);
      Parts.heading(s, 'Said <span class="hl">plainly.</span>', { x: 96, y: 170, w: 900, cls: 'l', at: 0.3 });
      Parts.check(s, 'No SOC 2, ISO 27001 or HIPAA certification', { x: 96, y: 460, at: 2.4, no: true });
      Parts.check(s, 'No support SLA', { x: 96, y: 550, at: 3.6, no: true });
      Parts.check(s, 'A human red-team engagement still outstanding', { x: 96, y: 640, at: 4.6, no: true });
      [[1060, 430, 24, 'of 24 adversarial attacks blocked', 7.6, (v) => Math.round(v) + '/24'], [1500, 430, 584, 'test functions across the engine', 8.0],
       [1060, 730, 20, 'model providers, one abstraction', 8.4], [1500, 730, 0, 'bytes leaving the network by default', 8.8]].forEach(([x, y, to, label, at, fmt]) => {
        const f = Parts.fig(s, { x, y, to, label, at, fmt }); f.querySelector('b').style.fontSize = '112px'; f.querySelector('span').style.maxWidth = '340px';
      });
    } },

    { beat: 'b13', build(s) {
      const logo = Parts.logo(s, { x: 0, y: 0, text: 'Titan', mark: 'titan', at: 0.2, size: 160 });
      logo.style.left = '50%'; logo.style.top = '36%'; logo.style.transform = 'translate(-50%,-50%)';
      s.at(0.2, 1.0, (p) => { logo.style.opacity = p; logo.style.transform = `translate(-50%,-50%) scale(${0.9 + 0.1 * ease.outBack(p)})`; });
      const h = Parts.heading(s, 'The agent harness for work that <span class="hl">cannot leave the building.</span>', { x: 210, y: 560, w: 1500, cls: 'm', at: 1.0 }); h.style.textAlign = 'center';
      const u = Parts.pill(s, 'zybuu.com/titan', { x: 800, y: 820, at: 4.2 }); u.style.fontSize = '30px';
    } },
  ];
};
