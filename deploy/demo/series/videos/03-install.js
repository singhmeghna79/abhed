// Video 3 — Installing Abhed, from a laptop to an air-gapped enclave.
window.VIDEOS['03-install'] = () => {
  const { fx, ease, el } = Motion;
  const title = (s, kicker, html, at = 0.1, cls = 'l', w = 1400) => { Parts.kicker(s, kicker, 96, 120, at); return Parts.heading(s, html, { x: 96, y: 170, w, cls, at: at + 0.2 }); };
  return [
    { beat: 'b1', build(s) {
      Parts.logo(s, { x: 96, y: 96, text: 'Abhed', mark: 'abhed', prod: 'Install', at: 0.2, size: 80 });
      Parts.heading(s, 'From a laptop to an <span class="hl">air-gapped rack.</span>', { x: 96, y: 300, w: 1500, cls: 'xl', at: 1.0 });
      Parts.para(s, 'Every supported path, in the order an enterprise walks them.', { x: 96, y: 700, w: 1200, at: 4.6, size: 36 });
      Parts.foot(s, 'ZYBUU · ABHED · SERIES 3 OF 4', 2.0);
    } },

    // b2: the shape — binary + database + three choices
    { beat: 'b2', build(s) {
      title(s, 'The shape of a deployment', 'One binary. <span class="hl">One database.</span>');
      Parts.diagram(s, { nodes: [
        { id: 't', x: 700, y: 480, w: 320, h: 120, html: 'abhed<small>one static binary</small>', cls: 'hot', at: 2.2 },
        { id: 'pg', x: 700, y: 760, w: 320, h: 110, html: 'Postgres 16<small>sessions · accounts · record</small>', at: 3.0 },
        { id: 'm', x: 1240, y: 340, w: 380, h: 110, html: 'Model endpoint<small>where the model runs</small>', at: 6.0 },
        { id: 'idp', x: 1240, y: 540, w: 380, h: 110, html: 'Identity<small>who signs people in</small>', at: 7.2 },
        { id: 'sb', x: 1240, y: 740, w: 380, h: 110, html: 'Sandbox tier<small>how far the agent reaches</small>', at: 8.4 },
      ], edges: [{ from: 't', to: 'pg', at: 3.2, curve: false }, { from: 't', to: 'm', at: 6.2 }, { from: 't', to: 'idp', at: 7.4 }, { from: 't', to: 'sb', at: 8.6 }] });
      Parts.para(s, 'Around it, three things <b>you choose.</b>', { x: 96, y: 520, w: 520, at: 5.4, size: 34 });
    } },

    // b3: three topologies
    { beat: 'b3', build(s) {
      title(s, 'Three topologies', 'Hosted. Private. <span class="hl">Air-gapped.</span>');
      const cards = [[ICONS.cloud, 'Hosted', 'Zybuu runs it for you. The fastest way to an agent under policy.', 2.4], [ICONS.building, 'Private', 'Your cloud or your data centre. Your identity provider, your model.', 4.6], [ICONS.lock, 'Air-gapped', 'A signed bundle carried in. Nothing ever calls out.', 7.2]];
      cards.forEach(([ic, t, d, at], i) => Parts.card(s, { x: 96 + i * 590, y: 470, w: 560, h: 290, icon: ic, title: t, text: d, at }));
      Parts.rule(s, { x: 96, y: 820, w: 1730, at: 10.6 });
      Parts.para(s, 'The binary and the guarantees are the same in all three.', { x: 96, y: 850, w: 1400, at: 10.9, size: 36 });
    } },

    // b4: requirements
    { beat: 'b4', build(s) {
      title(s, 'What you need', 'Sized by the model, <span class="hl">not by Abhed.</span>');
      const reqs = [['Host', 'Linux or macOS · container runtime optional', 1.6], ['Database', 'Postgres 16 — a plain application role, never a superuser', 2.8], ['Model endpoint', 'Ollama, vLLM, SGLang, TGI, llama.cpp — or a cloud provider you allow', 4.0], ['Sign-in', 'Local accounts, or OIDC with your identity provider', 5.6]];
      reqs.forEach(([t, d, at], i) => Parts.card(s, { x: 96, y: 440 + i * 128, w: 1130, h: 112, at, html: `<div style="display:flex;align-items:center;gap:30px"><span style="font-size:28px;font-weight:700;min-width:260px">${t}</span><span style="font-size:24px;color:var(--ink-2)">${d}</span></div>` }));
      Parts.fig(s, { x: 1340, y: 470, to: 2, label: 'GB of memory for the harness container itself', at: 8.4, fmt: (v) => Math.round(v) + ' GB' });
      Parts.pill(s, 'KV cache, not weights, is the binding constraint', { x: 1340, y: 760, at: 9.2 });
    } },

    // b5: from source
    { beat: 'b5', build(s) {
      title(s, 'Path 1 · from source', 'Go 1.26. <span class="hl">Three dependencies.</span>');
      Parts.terminal(s, { x: 96, y: 440, w: 1200, h: 520, title: '~ — zsh', at: 1.2, size: 25, lines: [
        ['cmd', '$ git clone <your mirror>/abhed && cd abhed', 1.6, true],
        ['cmd', '$ go build -o ~/.local/bin/abhed ./cmd/abhed', 3.4, true],
        ['ok', '  static binary · CGO_ENABLED=0 · pgx, x/crypto, x/term', 5.0],
        ['cmd', '$ ollama pull gemma4:26b', 6.0, true],
        ['cmd', '$ abhed init', 7.6, true],
        ['dim', '  wrote .abhed/config.json · detected http://localhost:11434/v1', 8.4],
        ['cmd', '$ abhed doctor', 9.4, true],
      ] });
      Parts.check(s, 'One binary, no runtime', { x: 1380, y: 500, at: 5.4 });
      Parts.check(s, 'Any model you host', { x: 1380, y: 590, at: 6.6 });
      Parts.check(s, 'Config detected, not guessed', { x: 1380, y: 680, at: 8.6 });
    } },

    // b6: doctor — real output
    { beat: 'b6', build(s) {
      title(s, 'abhed doctor', 'Ten seconds that <span class="hl">save a run.</span>');
      Parts.terminal(s, { x: 96, y: 400, w: 1240, h: 620, title: '~/work — abhed doctor', at: 1.0, size: 22, lines: [
        ['cmd', '$ abhed doctor', 1.2, true],
        ['dim', 'workspace   /workspace/tempconv', 2.2], ['dim', 'provider    local (ollama)', 2.35], ['dim', 'endpoint    http://models.internal:11434/v1', 2.5],
        ['dim', 'model       gemma4:26b', 2.65], ['dim', 'sandbox     process — process isolation via bwrap · workspace-scoped writes · no network', 3.4],
        ['dim', 'auth        local accounts (username and password)', 4.2], ['dim', 'storage     postgres (durable, tenant=default)', 4.9],
        ['dim', '            14 sessions · 1042 events persisted', 5.1], ['dim', 'skills      11 loaded', 5.4], ['', '', 5.6],
        ['ok', 'checking endpoint... ok', 8.2], ['ok', 'checking tool calling... ok', 10.6], ['dim', '  called glob with {"pattern":"*.go"}', 11.0], ['', '', 11.2], ['ok', 'Ready.', 12.6],
      ] });
      Parts.check(s, 'What is actually in force', { x: 1400, y: 470, at: 3.0 });
      Parts.check(s, 'A real completion', { x: 1400, y: 560, at: 8.4 });
      Parts.check(s, 'A real tool call back', { x: 1400, y: 650, at: 10.8 });
      Parts.callout(s, 'A model that cannot emit a tool call cannot drive an agent.', { x: 1400, y: 760, at: 13.0, w: 440 });
    } },

    // b7: container
    { beat: 'b7', build(s) {
      title(s, 'Path 2 · the container', 'One script. <span class="hl">A private network.</span>');
      Parts.terminal(s, { x: 96, y: 440, w: 900, h: 430, title: 'deploy — run.sh', at: 1.0, size: 23, lines: [
        ['cmd', '$ ./deploy/run.sh serve', 1.3, true], ['dim', 'creating volume abhed-workspace', 2.6], ['dim', 'creating volume abhed-state', 2.8], ['dim', 'creating volume abhed-db-data', 3.0],
        ['dim', 'starting abhed-db … ready', 3.6], ['ok', 'provisioned role abhed_app (NOSUPERUSER NOBYPASSRLS)', 4.6], ['ok', 'abhed listening on 127.0.0.1:8080', 5.6],
      ] });
      Parts.diagram(s, { nodes: [
        { id: 'net', x: 1100, y: 420, w: 720, h: 460, html: '', at: 6.0 },
        { id: 't', x: 1180, y: 520, w: 260, h: 110, html: 'abhed<small>127.0.0.1:8080</small>', cls: 'hot', at: 6.4 },
        { id: 'db', x: 1500, y: 520, w: 260, h: 110, html: 'abhed-db<small>no published port</small>', at: 6.9 },
        { id: 'v', x: 1180, y: 720, w: 580, h: 90, html: 'named volumes only — no bind mounts to the host', at: 7.8 },
      ], edges: [{ from: 't', to: 'db', at: 7.2, curve: false }] });
      const lab = Parts.pill(s, 'network: abhed-net', { x: 1120, y: 396, at: 6.2 });
      Parts.check(s, 'Abhed is the only client of the database', { x: 96, y: 920, at: 9.6 });
    } },

    // b8: hardening
    { beat: 'b8', build(s) {
      title(s, 'Locked down by default', 'The container <span class="hl">cannot be talked out of it.</span>');
      const items = ['--user 10001 · non-root', '--cap-drop ALL', '--read-only root filesystem', '--security-opt no-new-privileges', '--memory 2g · --cpus 2 · --pids 512', 'tmpfs /tmp noexec,nosuid', 'config.json mounted read-only', 'skills mounted read-only'];
      items.forEach((t, i) => { const n = Parts.pill(s, t, { x: 96 + (i % 2) * 640, y: 470 + Math.floor(i / 2) * 84, at: 1.4 + i * 0.9 }); n.style.fontSize = '24px'; });
      Parts.callout(s, 'A managed config at /etc/abhed wins over every other source — bypass mode can be refused, and the policy cannot be edited from inside.', { x: 1330, y: 500, w: 500, at: 8.6 });
    } },

    // b9: two roles
    { beat: 'b9', build(s) {
      title(s, 'Postgres, two roles', 'Row-level security is the <span class="hl">tenant boundary.</span>');
      Parts.diagram(s, { nodes: [
        { id: 'adm', x: 200, y: 520, w: 380, h: 120, html: 'abhed_admin<small>superuser · provisions only</small>', at: 1.8 },
        { id: 'app', x: 760, y: 520, w: 380, h: 120, html: 'abhed_app<small>NOSUPERUSER · NOBYPASSRLS · owns the tables</small>', cls: 'ok', at: 3.4 },
        { id: 'db', x: 1320, y: 520, w: 380, h: 120, html: 'FORCE ROW LEVEL SECURITY<small>every table · every query</small>', cls: 'hot', at: 5.0 },
      ], edges: [{ from: 'adm', to: 'app', at: 3.6, curve: false }, { from: 'app', to: 'db', at: 5.2, curve: false }] });
      Parts.check(s, 'Abhed refuses to start as a superuser or BYPASSRLS role', { x: 200, y: 760, at: 7.4 });
      Parts.check(s, 'Postgres exempts a superuser from RLS — even with FORCE', { x: 200, y: 850, at: 9.6 });
    } },

    // b10: config
    { beat: 'b10', build(s) {
      title(s, 'Configuration', 'One JSON file. <span class="hl">No secrets in it.</span>');
      Parts.terminal(s, { x: 96, y: 400, w: 1180, h: 620, title: '/etc/abhed/config.json', at: 1.0, size: 21, lines: [
        ['cmd', '{', 1.2],
        ['tool', '  "model":   { "default": "onprem", "providers": { "onprem": {', 2.0], ['dim', '                 "type": "openai-compatible", "base_url": "http://vllm.internal:8000/v1",', 2.2],
        ['dim', '                 "model": "Qwen/Qwen3-32B", "api_key_env": "ABHED_API_KEY" } } },', 2.4],
        ['tool', '  "sandbox": { "min_tier": "container", "allow_network": false },', 3.6],
        ['tool', '  "storage": { "driver": "postgres", "tenant": "default" },', 4.8],
        ['tool', '  "auth":    { "mode": "oidc", "issuer": "https://idp.internal/realms/eng", "groups_claim": "groups" },', 5.8],
        ['tool', '  "permissions": { "deny": ["bash(rm -rf /*)", "read(**/.ssh/**)", "read(**/*.pem)"] },', 7.0],
        ['tool', '  "limits":  { "max_turns": 100, "max_subagents": 20 }', 8.2],
        ['cmd', '}', 8.4],
        ['ok', '# ABHED_DATABASE_URL and ABHED_API_KEY come from the environment', 9.8],
      ] });
      Parts.check(s, 'Providers', { x: 1340, y: 470, at: 2.0 }); Parts.check(s, 'Sandbox & egress', { x: 1340, y: 550, at: 3.8 });
      Parts.check(s, 'Store', { x: 1340, y: 630, at: 5.0 }); Parts.check(s, 'Auth', { x: 1340, y: 710, at: 6.0 });
      Parts.check(s, 'Absolute deny rules', { x: 1340, y: 790, at: 7.2 }); Parts.check(s, 'Limits', { x: 1340, y: 870, at: 8.4 });
    } },

    // b11: sign-in
    { beat: 'b11', build(s) {
      title(s, 'Sign-in', 'Local accounts, or <span class="hl">your identity provider.</span>');
      Parts.card(s, { x: 96, y: 460, w: 700, h: 300, icon: ICONS.user, title: 'Local accounts', text: 'Issued by an administrator: <span class="mono">abhed user add</span>. Invite-only registration, never open.', at: 1.6 });
      Parts.card(s, { x: 830, y: 460, w: 990, h: 300, icon: ICONS.shield, title: 'OpenID Connect', text: 'Groups from the provider decide who administers. Documented recipes:', at: 3.8 });
      ['Keycloak', 'Okta', 'Microsoft Entra ID', 'Auth0', 'Google Workspace'].forEach((p, i) => Parts.pill(s, p, { x: 872 + i * 190, y: 690, at: 5.4 + i * 0.5 }));
      Parts.check(s, 'Local and OIDC can run side by side', { x: 96, y: 830, at: 9.6 });
      Parts.check(s, 'Air-gapped OIDC: a JWKS URL inside the enclave', { x: 96, y: 910, at: 10.6 });
    } },

    // b12: bundle
    { beat: 'b12', build(s) {
      title(s, 'Path 3 · air-gapped', 'Build a <span class="hl">signed bundle.</span>');
      Parts.terminal(s, { x: 96, y: 440, w: 1000, h: 480, title: 'connected build host', at: 1.0, size: 23, lines: [
        ['cmd', '$ ./scripts/build-bundle.sh -v 0.1.0 -k release.key', 1.3, true],
        ['dim', 'bin/     abhed · abhed-bench  ×  linux/amd64 linux/arm64 darwin/arm64 darwin/amd64', 3.6],
        ['dim', 'vendor/  Go dependencies, pinned', 4.6], ['dim', 'docs/  schema/  config/config.example.json', 5.2],
        ['dim', 'sbom.json · BUILDINFO · SHA256SUMS', 6.4],
        ['ok', 'abhed-0.1.0.tar.gz  +  .sha256  +  .sig', 7.6],
      ] });
      const items = [['Static binaries', 'four platforms', 3.6], ['Vendored deps', 'no fetch at install', 4.6], ['SBOM', 'what is inside, machine-readable', 6.4], ['Detached signature', 'OpenSSL, SHA-256, your key', 7.8]];
      items.forEach(([t, d, at], i) => Parts.card(s, { x: 1140, y: 440 + i * 122, w: 680, h: 106, at, html: `<div style="display:flex;align-items:baseline;gap:20px"><b style="font-size:26px">${t}</b><span style="font-size:22px;color:var(--ink-2)">${d}</span></div>` }));
    } },

    // b13: verify
    { beat: 'b13', build(s) {
      title(s, 'Inside the enclave', 'Verify first. <span class="hl">Install second.</span>');
      Parts.terminal(s, { x: 96, y: 440, w: 1000, h: 480, title: 'air-gapped target — no route out', at: 1.0, size: 23, lines: [
        ['cmd', '$ ./scripts/verify-bundle.sh abhed-0.1.0.tar.gz release.pub', 1.3, true],
        ['ok', '✓ archive sha256 matches .sha256', 4.2], ['ok', '✓ signature verified against release.pub', 5.4], ['ok', '✓ SHA256SUMS: every file OK', 6.6], ['ok', '✓ install.sh executable · binaries present', 7.2],
        ['cmd', '$ tar xzf abhed-0.1.0.tar.gz && ./install.sh', 9.0, true], ['dim', 'installed to /opt/abhed', 10.6],
      ] });
      const steps = [['1', 'archive hash', 4.2], ['2', 'signature', 5.4], ['3', 'every file', 6.6]];
      steps.forEach(([n, t, at], i) => Parts.card(s, { x: 1140 + i * 230, y: 460, w: 210, h: 150, at, html: `<div class="hl" style="font-size:54px;font-weight:800">${n}</div><div style="font-size:22px;color:var(--ink-2)">${t}</div>` }));
      Parts.callout(s, 'A bundle that fails any check never reaches the installer.', { x: 1140, y: 660, w: 640, at: 7.8 });
    } },

    // b14: same install, offline
    { beat: 'b14', build(s) {
      title(s, 'The same product, offline', 'Nothing to reach. <span class="hl">Nothing to leak.</span>');
      Parts.diagram(s, { nodes: [
        { id: 'enc', x: 380, y: 400, w: 1160, h: 500, html: '', at: 1.0 },
        { id: 't', x: 480, y: 520, w: 300, h: 110, html: 'abhed<small>/opt/abhed</small>', cls: 'hot', at: 1.6 },
        { id: 'db', x: 830, y: 520, w: 300, h: 110, html: 'Postgres 16', at: 2.2 },
        { id: 'm', x: 1180, y: 520, w: 300, h: 110, html: 'vLLM · your GPUs', at: 2.8 },
        { id: 'idp', x: 830, y: 740, w: 300, h: 90, html: 'IdP · JWKS inside', at: 3.6 },
      ], edges: [{ from: 't', to: 'db', at: 2.3, curve: false }, { from: 't', to: 'm', at: 2.9 }, { from: 't', to: 'idp', at: 3.8 }] });
      Parts.pill(s, 'enclave · no route to the internet', { x: 400, y: 376, at: 1.2 });
      Parts.terminal(s, { x: 1560, y: 400, w: 300, h: 150, title: 'config', at: 6.4, size: 20, lines: [['tool', '"server": {', 6.6], ['cmd', '  "home_url": ""', 6.9], ['tool', '}', 7.1]] });
      Parts.check(s, 'The console shows no link it cannot follow', { x: 380, y: 940, at: 7.6 });
    } },

    // b15: day two
    { beat: 'b15', build(s) {
      title(s, 'Day two', 'Short, <span class="hl">on purpose.</span>');
      const rows = [['abhed user add <name>', 'issue an account, or connect OIDC', 1.6], ['"telemetry": { "enabled": true, "endpoint": "http://otel:4318" }', 'traces into your collector', 3.4], ['"schedules": [{ "cron": "@hourly", … }]', 'recurring work, on the record', 5.0], ['replace one binary', 'the schema migrates itself on start; every migration is idempotent', 6.8]];
      rows.forEach(([c, d, at], i) => Parts.card(s, { x: 96, y: 440 + i * 120, w: 1700, h: 104, at, html: `<div style="display:flex;align-items:center;gap:30px"><span class="mono" style="font-size:24px;color:var(--live);min-width:760px">${c}</span><span style="font-size:24px;color:var(--ink-2)">${d}</span></div>` }));
    } },

    { beat: 'b16', build(s) {
      const logo = Parts.logo(s, { x: 0, y: 0, text: 'Abhed', mark: 'abhed', at: 0.2, size: 150 });
      logo.style.left = '50%'; logo.style.top = '34%'; logo.style.transform = 'translate(-50%,-50%)';
      s.at(0.2, 1.0, (p) => { logo.style.opacity = p; logo.style.transform = `translate(-50%,-50%) scale(${0.9 + 0.1 * ease.outBack(p)})`; });
      ['One binary', 'One database', 'Three topologies', 'The same guarantees'].forEach((t, i) => { const n = Parts.pill(s, t, { x: 380 + i * 300, y: 560, at: 1.2 + i * 0.7 }); n.style.fontSize = '26px'; });
      const u = Parts.pill(s, 'abhed.zybuu.com/docs', { x: 790, y: 720, at: 5.2 }); u.style.fontSize = '30px';
    } },
  ];
};
