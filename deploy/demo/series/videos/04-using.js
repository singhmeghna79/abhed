// Video 4 — Using Titan: the console, then the command line. Real footage
// (assets.json from footage.mjs) and real captured CLI output (cli/*.txt).
window.VIDEOS['04-using'] = () => {
  const { fx, ease, el } = Motion;
  const title = (s, kicker, html, at = 0.1, cls = 'l', w = 1500) => { Parts.kicker(s, kicker, 96, 120, at); return Parts.heading(s, html, { x: 96, y: 170, w, cls, at: at + 0.2 }); };
  // classify a captured CLI transcript into terminal lines
  const cliLines = (text, start, step = 0.55, max = 60, width = 96) => {
    const out = []; let t = start;
    (text || '').split('\n').slice(0, max).forEach((raw) => {
      let l = raw.replace(/\x1b\[[0-9;]*m/g, '');
      if (l.length > width) l = l.slice(0, width - 1) + '…';
      let cls = '';
      if (/^\s*●/.test(l)) cls = 'tool'; else if (/^\s*└/.test(l)) cls = 'res'; else if (/^\s*│|✕|rejected|refus|error/i.test(l)) cls = 'warn';
      else if (/^\s*\$/.test(l)) cls = 'cmd'; else if (/ok\b|passed|OK$/.test(l)) cls = 'ok'; else if (l.trim() === '') cls = 'dim';
      out.push([cls, l, t]); t += /^\s*●/.test(l) ? step * 1.6 : step * 0.6;
    });
    return out;
  };
  const screenFull = (s, key, at, cap, from = 0, speed = 1) => Parts.screen(s, { x: 210, y: 330, w: 1500, h: 844, src: key, at, cap, from, speed });
  const screenSide = (s, key, at, cap, from = 0, speed = 1) => Parts.screen(s, { x: 560, y: 236, w: 1300, h: 731, src: key, at, cap, from, speed });
  // console scenes: a narrow left column, the footage large on the right
  const side = (s, kicker, html, checks, callout) => {
    Parts.kicker(s, kicker, 96, 130, 0.1);
    Parts.heading(s, html, { x: 96, y: 190, w: 420, cls: 's', at: 0.3 });
    checks.forEach(([t, at], i) => { const c = Parts.check(s, t, { x: 96, y: 500 + i * 96, at }); c.style.fontSize = '25px'; c.style.width = '430px'; });
    if (callout) Parts.callout(s, callout[0], { x: 96, y: 500 + checks.length * 96 + 10, at: callout[1], w: 430 });
  };

  return [
    { beat: 'b1', build(s) {
      Parts.logo(s, { x: 96, y: 96, text: 'Titan', mark: 'titan', prod: 'Using it', at: 0.2, size: 80 });
      Parts.heading(s, 'The console, then the <span class="hl">command line.</span>', { x: 96, y: 300, w: 1500, cls: 'xl', at: 1.0 });
      Parts.para(s, 'A real deployment, recorded as it ran. Nothing here is a mock-up.', { x: 96, y: 700, w: 1300, at: 8.0, size: 36 });
      Parts.foot(s, 'ZYBUU · TITAN · SERIES 4 OF 4', 2.0);
    } },

    { beat: 'b2', build(s) {
      side(s, 'The console · sign in', 'Accounts are <span class="hl">issued.</span>', [['Never self-served', 3.2], ['One account, one tenant', 5.4], ['Examples drawn from real tools', 11.0]]);
      screenSide(s, 'signin', 0.8, '<b>titan.zybuu.com</b> · local accounts · one tenant', 0, 1);
    } },

    { beat: 'b3', build(s) {
      side(s, 'Ask it something', 'Plan mode: <span class="hl">read-only.</span>', [['Arguments and results, live', 6.0], ['A refused call is shown, not hidden', 10.5]], ['This transcript <b>is</b> the audit record.', 15.0]);
      screenSide(s, 'ask', 0.6, 'plan mode · every tool call as it happens', 3, 1.35);
    } },

    { beat: 'b4', build(s) {
      side(s, 'Now a change', 'A write <span class="hl">waits for a person.</span>', [['Exactly what will be written', 5.0], ['Approve or deny — recorded with your name', 9.0], ['Then the agent finishes the job', 13.5]]);
      screenSide(s, 'approve', 0.6, 'default mode · approval card · decision recorded', 6, 1.6);
    } },

    { beat: 'b5', build(s) {
      side(s, 'Attach a document', 'Inside the <span class="hl">sandbox.</span>', [['Uploaded into the session', 4.0], ['Read inside the sandbox', 7.5], ['Nothing leaves the deployment', 10.5]]);
      screenSide(s, 'attach', 0.6, 'attachments · uploaded into the session workspace', 2, 1.6);
    } },

    { beat: 'b6', build(s) {
      side(s, 'Replay, search, delete', 'From the <span class="hl">append-only record.</span>', [['Replay any chat, step by step', 3.0], ['Search the rail; grouped by day', 6.0], ['Delete: hidden for you, kept for audit', 10.0]]);
      screenSide(s, 'replay', 0.6, 'replay · search · day groups · delete with confirmation', 0, 1.1);
    } },

    { beat: 'b7', build(s) {
      side(s, 'The deployment page', 'What is <span class="hl">actually in force.</span>', [['Model and context window', 3.0], ['Isolation tier', 5.1], ['Store and sign-in mode', 7.2], ['Egress: allowed or not', 9.3], ['Every boundary, held or not', 11.4]]);
      screenSide(s, 'overview', 0.6, 'model · isolation · store · sign-in · egress · containment', 0, 1);
    } },

    { beat: 'b8', build(s) {
      title(s, 'The command line', 'Same binary. <span class="hl">Same rules.</span>');
      Parts.terminal(s, { x: 96, y: 470, w: 1000, h: 380, title: '~/work — zsh', at: 1.2, size: 26, lines: [
        ['cmd', '$ titan', 1.6, true], ['dim', '  interactive · line editor · slash commands · steer mid-run', 2.8],
        ['cmd', '$ titan -p "…"', 4.8, true], ['dim', '  headless · one prompt · typed exit code', 5.8],
      ] });
      Parts.check(s, 'The same policy engine', { x: 1180, y: 520, at: 3.4 });
      Parts.check(s, 'The same record', { x: 1180, y: 600, at: 4.2 });
      Parts.check(s, 'Exit codes a pipeline can act on', { x: 1180, y: 680, at: 7.0 });
    } },

    { beat: 'b9', build(s) {
      title(s, 'Headless', 'One prompt. <span class="hl">Real work.</span>', 0.1, 'm', 700);
      const lines = [['cmd', '$ titan -mode accept-edits -p "Add f_to_c to tempconv.py with a docstring, add unittest coverage for both directions, then run the suite."', 0.9, true]]
        .concat(cliLines(ASSETS.run1_head, 4.8, 0.7, 12));
      Parts.terminal(s, { x: 96, y: 400, w: 1240, h: 640, title: '/workspace/tempconv — titan -p', at: 0.6, size: 22, lines });
      Parts.check(s, 'glob → read → edit → write', { x: 1400, y: 470, at: 7.0 });
      Parts.callout(s, 'A relative path is refused — and the message says what would have worked.', { x: 1400, y: 580, at: 10.0, w: 440 });
      Parts.check(s, 'The agent recovers on its own', { x: 1400, y: 780, at: 13.5 });
    } },

    { beat: 'b10', build(s) {
      title(s, 'The policy does its job', 'Nobody to ask? <span class="hl">The answer is no.</span>', 0.1, 'm', 900);
      const lines = cliLines(ASSETS.run1_tail, 1.2, 0.6, 14);
      Parts.terminal(s, { x: 96, y: 400, w: 1240, h: 640, title: '/workspace/tempconv — titan -p (continued)', at: 0.6, size: 22, lines });
      Parts.check(s, 'accept-edits: edits yes, commands still ask', { x: 1400, y: 470, at: 4.0 });
      Parts.check(s, 'Headless with no approver: refuse', { x: 1400, y: 560, at: 7.0 });
      Parts.check(s, 'Reported, not pretended', { x: 1400, y: 650, at: 11.0 });
    } },

    { beat: 'b11', build(s) {
      title(s, 'Grant exactly what the job needs', 'An allow rule, <span class="hl">and nothing more.</span>', 0.1, 'm', 900);
      const lines = [['cmd', "$ titan -mode accept-edits -allow 'bash(python3 -m unittest*)' -p \"Run the test suite and report the result.\"", 0.9, true]]
        .concat(cliLines(ASSETS.run2, 4.4, 0.6, 18));
      Parts.terminal(s, { x: 96, y: 400, w: 1240, h: 640, title: '/workspace/tempconv — titan -p', at: 0.6, size: 22, lines });
      Parts.check(s, 'Allowed: that command, that prefix', { x: 1400, y: 470, at: 5.0 });
      Parts.check(s, 'The suite runs and passes', { x: 1400, y: 560, at: 9.5 });
      Parts.check(s, 'Deny rules still hold — in every mode', { x: 1400, y: 650, at: 12.5 });
    } },

    { beat: 'b12', build(s) {
      title(s, 'Interactive', 'Steer it <span class="hl">mid-run.</span>', 0.1, 'm', 700);
      const cmds = [['/mode plan', 'read-only for this session', 2.2], ['/model claude', 'switch provider mid-session', 3.4], ['/undo · /diff', 'see and revert what changed', 4.6], ['/compact', 'summarise, keep the recent turns', 5.8], ['/fork 12', 'branch from any earlier step', 6.8], ['/export report.html', 'a self-contained transcript', 8.2], ['/resume s-mrtwxf', 'pick a session back up by id', 9.6]];
      cmds.forEach(([c, d, at], i) => Parts.card(s, { x: 96, y: 400 + i * 88, w: 1100, h: 76, at, html: `<div style="display:flex;align-items:center;gap:26px"><span class="mono" style="font-size:24px;color:var(--live);min-width:330px">${c}</span><span style="font-size:23px;color:var(--ink-2)">${d}</span></div>` }).style.padding = '20px 30px');
      Parts.callout(s, 'Type while it works: the steer lands at the <b>next step</b>, not as an interrupt.', { x: 1260, y: 480, w: 560, at: 12.4 });
    } },

    { beat: 'b13', build(s) {
      title(s, 'Pipelines, other languages, your own code', 'Three more <span class="hl">surfaces.</span>', 0.1, 'm', 900);
      const json = cliLines(ASSETS.run3, 1.6, 0.5, 6, 78);
      Parts.terminal(s, { x: 96, y: 400, w: 860, h: 300, title: 'titan -output-format json', at: 1.0, size: 19, lines: [['cmd', '$ titan -mode plan -output-format json -p "…"', 1.2]].concat(json.map(([c, l, t]) => [c === 'cmd' ? 'dim' : c, l, t + 0.6])) });
      const rpc = cliLines(ASSETS.run4, 6.4, 0.5, 6, 78);
      Parts.terminal(s, { x: 96, y: 730, w: 860, h: 300, title: 'titan rpc — JSONL over stdio', at: 6.0, size: 19, lines: [['cmd', '{"method":"start","params":{"prompt":"List the files…","mode":"plan"}}', 6.2]].concat(rpc.map(([c, l, t]) => ['dim', l, t + 0.6])) });
      Parts.terminal(s, { x: 1000, y: 400, w: 820, h: 630, title: 'main.go — the SDK', at: 11.0, size: 20, lines: [
        ['cmd', 'a, _ := titan.New(ctx, titan.Options{', 11.2], ['dim', '    Workspace: "/srv/work",', 11.4], ['dim', '    Deny:      []string{"bash(rm -rf *)"},', 11.6],
        ['dim', '    Approve:   askYourUser,', 11.8], ['dim', '})', 12.0], ['cmd', '', 12.1], ['cmd', 'var out Review', 12.6],
        ['tool', 'err := a.RunJSON(ctx, prompt, schema, &out)', 12.9], ['ok', '// validated in the loop — never invalid JSON', 14.2],
        ['dim', '', 14.4], ['dim', 'a.Steer("focus on pkg/auth")   a.Fork(12)', 15.0], ['dim', 'a.Events()   a.Usage()   a.ExportHTML()', 15.4],
      ] });
    } },

    { beat: 'b14', build(s) {
      title(s, 'And beyond one agent', 'Parallel. Scheduled. <span class="hl">Extended.</span>');
      const cards = [[ICONS.branch, 'Parallel subagents', 'Each in its own git worktree. Two writers never collide.', 1.8], [ICONS.clock, 'Scheduled runs', 'Recurring work, on a clock, as sessions on the record.', 4.2], [ICONS.plug, 'Skills · extensions · MCP', 'Extend what it can do — never what it is allowed to do.', 7.0]];
      cards.forEach(([ic, t, d, at], i) => Parts.card(s, { x: 96 + i * 590, y: 470, w: 560, h: 290, icon: ic, title: t, text: d, at }));
    } },

    { beat: 'b15', build(s) {
      const logo = Parts.logo(s, { x: 0, y: 0, text: 'Titan', mark: 'titan', at: 0.2, size: 150 });
      logo.style.left = '50%'; logo.style.top = '34%'; logo.style.transform = 'translate(-50%,-50%)';
      s.at(0.2, 1.0, (p) => { logo.style.opacity = p; logo.style.transform = `translate(-50%,-50%) scale(${0.9 + 0.1 * ease.outBack(p)})`; });
      ['A console for people', 'A command line for engineers', 'One record of everything'].forEach((t, i) => { const n = Parts.pill(s, t, { x: 330 + i * 440, y: 560, at: 1.6 + i * 1.2 }); n.style.fontSize = '26px'; });
      const u = Parts.pill(s, 'zybuu.com/titan · titan.zybuu.com/docs', { x: 640, y: 720, at: 6.0 }); u.style.fontSize = '30px';
    } },
  ];
};
