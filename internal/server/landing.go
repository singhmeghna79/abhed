package server

// landingHTML is Titan's front door.
//
// It has one job that a marketing page does not: tell you what THIS deployment
// is. Model, sandbox tier, storage durability, whether the agent can reach the
// internet, how many sessions have run. Those are the facts an operator needs
// before typing anything, and the reason the page reads its own /v1/overview
// rather than shipping static copy.
//
// Same constraint as the console: one self-contained document, no CDN, no build
// step, because it ships inside an air-gapped bundle.
const landingHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Titan</title>
<style>
:root{
  --bg:#F5F7FA; --surface:#FFFFFF; --sunken:#EDF1F6;
  --line:#DCE3EC; --line-strong:#C4CFDD;
  --ink:#0F141B; --ink-2:#3A4757; --muted:#697786;
  --accent:#1F6FB8; --accent-soft:#E3EEF8;
  --ok:#1A7F4B; --ok-bg:#E3F3EA; --warn:#9A6A16; --warn-bg:#FAF0DC;
  --mono:ui-monospace,SFMono-Regular,"SF Mono",Menlo,Consolas,monospace;
  --sans:-apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,Roboto,sans-serif;
}
@media (prefers-color-scheme:dark){
  :root:not([data-theme="light"]){
    --bg:#0B0E13; --surface:#141922; --sunken:#0F141C;
    --line:#252D3A; --line-strong:#333D4D;
    --ink:#E8EDF4; --ink-2:#BAC6D4; --muted:#8A96A8;
    --accent:#4C8FD6; --accent-soft:#16283C;
    --ok:#3FAF6C; --ok-bg:#0F2419; --warn:#D4A03C; --warn-bg:#241C0C;
  }
}
:root[data-theme="dark"]{
  --bg:#0B0E13; --surface:#141922; --sunken:#0F141C;
  --line:#252D3A; --line-strong:#333D4D;
  --ink:#E8EDF4; --ink-2:#BAC6D4; --muted:#8A96A8;
  --accent:#4C8FD6; --accent-soft:#16283C;
  --ok:#3FAF6C; --ok-bg:#0F2419; --warn:#D4A03C; --warn-bg:#241C0C;
}

*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font-family:var(--sans);
  font-size:14px;line-height:1.6;-webkit-font-smoothing:antialiased}
a{color:var(--accent)}
:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:4px}

.wrap{max-width:940px;margin:0 auto;padding:0 26px}

/* ---------------------------------------------------------------- masthead */
header{border-bottom:1px solid var(--line);background:var(--surface)}
.bar{display:flex;align-items:center;gap:10px;height:52px}
.bar .mark{width:20px;height:20px;fill:var(--accent);flex:none}
.bar b{font-size:15px;font-weight:650;letter-spacing:-.01em}
.bar .sub{font-family:var(--mono);font-size:10.5px;color:var(--muted)}
.bar .spacer{flex:1}
.bar .who{font-family:var(--mono);font-size:11px;color:var(--ink-2);
  background:var(--sunken);border:1px solid var(--line);border-radius:11px;
  padding:2px 9px}

/* ---------------------------------------------------------------- hero */
.hero{padding:52px 0 40px;display:grid;grid-template-columns:1fr auto;
  gap:40px;align-items:center}
.hero h1{margin:0 0 10px;font-size:31px;line-height:1.2;letter-spacing:-.022em;
  font-weight:640;text-wrap:balance}
.hero p{margin:0 0 22px;font-size:15px;color:var(--ink-2);max-width:52ch}
.cta{display:flex;gap:10px;align-items:center;flex-wrap:wrap}
.btn{display:inline-flex;align-items:center;gap:8px;background:var(--accent);
  color:#fff;border:1px solid var(--accent);border-radius:8px;padding:10px 18px;
  font-size:14px;font-weight:600;text-decoration:none;cursor:pointer;
  transition:filter .14s,transform .14s}
.btn:hover{filter:brightness(1.08);transform:translateY(-1px)}
.btn.ghost{background:var(--surface);color:var(--ink);border-color:var(--line)}
.btn.ghost:hover{border-color:var(--accent);filter:none}
.note{font-family:var(--mono);font-size:11px;color:var(--muted)}

/* The column, drawn large. Same three strokes as the CLI banner. */
.emblem{width:120px;height:120px;fill:var(--accent);opacity:.16;flex:none}

/* ---------------------------------------------------------------- panels */
h2{margin:0 0 14px;font-family:var(--mono);font-size:10.5px;letter-spacing:.13em;
  text-transform:uppercase;color:var(--muted);font-weight:600}
section{padding-bottom:34px}

.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(216px,1fr));gap:12px}
.card{background:var(--surface);border:1px solid var(--line);border-radius:9px;
  padding:14px 16px}
.card .k{font-family:var(--mono);font-size:9.5px;letter-spacing:.09em;
  text-transform:uppercase;color:var(--muted);margin-bottom:6px}
.card .v{font-size:14.5px;font-weight:560;overflow-wrap:anywhere}
.card .d{font-family:var(--mono);font-size:10.5px;color:var(--muted);margin-top:4px}

.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:12px}
.stat{background:var(--surface);border:1px solid var(--line);border-radius:9px;
  padding:14px 16px}
.stat .n{font-family:var(--mono);font-size:25px;font-weight:600;line-height:1.1;
  font-variant-numeric:tabular-nums;letter-spacing:-.02em}
.stat .l{font-family:var(--mono);font-size:10px;letter-spacing:.08em;
  text-transform:uppercase;color:var(--muted);margin-top:5px}

.chip{display:inline-flex;align-items:center;gap:5px;font-family:var(--mono);
  font-size:10.5px;padding:2px 8px;border-radius:4px;white-space:nowrap}
.chip.on{background:var(--ok-bg);color:var(--ok)}
.chip.off{background:var(--sunken);color:var(--muted)}
.chip.warn{background:var(--warn-bg);color:var(--warn)}

.tools{display:flex;flex-wrap:wrap;gap:6px}
.tool{font-family:var(--mono);font-size:11px;background:var(--sunken);
  border:1px solid var(--line);border-radius:5px;padding:3px 9px;color:var(--ink-2)}

footer{border-top:1px solid var(--line);padding:20px 0 30px;
  font-family:var(--mono);font-size:10.5px;color:var(--muted);
  display:flex;gap:18px;flex-wrap:wrap}

@media (max-width:760px){
  .hero{grid-template-columns:1fr}
  .emblem{display:none}
}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
</style>
</head>
<body>

<header>
  <div class="wrap bar">
    <svg class="mark" viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="3" width="18" height="3" rx="1"/>
      <rect x="9" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
      <rect x="11.7" y="7.5" width="1.6" height="9" rx=".6"/>
      <rect x="14.4" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
      <rect x="3" y="18" width="18" height="3" rx="1"/>
    </svg>
    <b>Titan</b>
    <span class="sub">on-prem coding agent</span>
    <span class="spacer"></span>
    <span class="who" id="who" hidden></span>
  </div>
</header>

<main class="wrap">
  <div class="hero">
    <div>
      <h1 id="headline">An agent that runs entirely on your own infrastructure.</h1>
      <p id="pitch">Point it at any reasoning model you host. Nothing leaves your
        network unless you configure it to.</p>
      <div class="cta" id="cta"></div>
    </div>
    <svg class="emblem" viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="3" width="18" height="3" rx="1"/>
      <rect x="9" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
      <rect x="11.7" y="7.5" width="1.6" height="9" rx=".6"/>
      <rect x="14.4" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
      <rect x="3" y="18" width="18" height="3" rx="1"/>
    </svg>
  </div>

  <section>
    <h2>This deployment</h2>
    <div class="grid" id="facts"></div>
  </section>

  <section>
    <h2>Activity</h2>
    <div class="stats" id="stats"></div>
  </section>

  <section>
    <h2>Tools available to the agent</h2>
    <div class="tools" id="tools"></div>
  </section>
</main>

<footer class="wrap">
  <span id="ver">titan</span>
  <span>every action is recorded and replayable</span>
  <span>tool output is treated as data, never instructions</span>
</footer>

<script>
"use strict";
const $ = id => document.getElementById(id);

function el(tag, cls, text){
  const n = document.createElement(tag);
  if(cls) n.className = cls;
  if(text !== undefined) n.textContent = text;
  return n;
}

function card(k, v, detail, chipClass){
  const c = el('div','card');
  c.appendChild(el('div','k',k));
  if(chipClass){
    const row = el('div','v');
    row.appendChild(el('span','chip ' + chipClass, v));
    c.appendChild(row);
  }else{
    c.appendChild(el('div','v',v));
  }
  if(detail) c.appendChild(el('div','d',detail));
  return c;
}

function stat(n, label){
  const s = el('div','stat');
  s.appendChild(el('div','n',String(n)));
  s.appendChild(el('div','l',label));
  return s;
}

async function load(){
  let o;
  try{
    const r = await fetch('/v1/overview');
    o = await r.json();
  }catch{
    $('pitch').textContent = 'Cannot reach the Titan server. Check that it is running.';
    return;
  }

  // ---- who, and where to go next
  const cta = $('cta');
  cta.textContent = '';

  if(o.sign_in_url && !o.authenticated){
    // A configured deployment: the front door is a sign-in.
    $('headline').textContent = 'Sign in to Titan';
    $('pitch').textContent =
      'This deployment uses your organisation’s identity provider. ' +
      'Sessions are scoped to your tenant and every action is recorded.';
    const a = el('a','btn','Sign in');
    a.href = o.sign_in_url;
    cta.appendChild(a);
    cta.appendChild(el('span','note','via ' + o.auth_mode.toUpperCase()));
  }else{
    const a = el('a','btn', o.authenticated ? 'Open console' : 'Start working');
    a.href = '/console';
    cta.appendChild(a);
    if(o.auth_mode === 'none'){
      // Say plainly that this instance has no accounts, rather than showing a
      // sign-in button that leads nowhere.
      cta.appendChild(el('span','note','no sign-in on this instance · single tenant'));
    }
  }

  if(o.authenticated){
    $('who').textContent = o.user + (o.tenant ? ' · ' + o.tenant : '');
    $('who').hidden = false;
  }

  // ---- what this deployment actually is
  const f = $('facts');
  f.textContent = '';
  f.appendChild(card('Model', o.model,
    o.context_window ? o.context_window.toLocaleString() + ' token context' : ''));
  f.appendChild(card('Workspace', shortPath(o.workspace), 'the only path the agent can reach'));
  f.appendChild(card('Sandbox', o.sandbox,
    o.sandbox_network ? 'network allowed' : 'no network access',
    o.sandbox === 'none' ? 'warn' : 'on'));
  f.appendChild(card('Storage', o.durable ? 'postgres' : 'in-memory',
    o.storage, o.durable ? 'on' : 'off'));
  f.appendChild(card('Authentication', o.auth_mode,
    o.auth_mode === 'none' ? 'single tenant, no accounts' : 'identity provider configured',
    o.auth_mode === 'none' ? 'off' : 'on'));
  f.appendChild(card('Web search', o.web_search,
    o.web_search === 'disabled' ? 'the agent stays offline' : 'the agent can reach the internet',
    o.web_search === 'disabled' ? 'off' : 'warn'));

  // ---- activity
  const st = $('stats');
  st.textContent = '';
  st.appendChild(stat(o.sessions ?? 0, 'sessions'));
  st.appendChild(stat(o.events ?? 0, 'events recorded'));
  st.appendChild(stat(o.running ?? 0, 'running now'));
  st.appendChild(stat((o.tools || []).length, 'tools'));
  if(o.mcp_servers) st.appendChild(stat(o.mcp_servers, 'mcp servers'));

  // ---- the agent's actual capabilities, not a feature list
  const t = $('tools');
  t.textContent = '';
  for(const name of (o.tools || [])) t.appendChild(el('span','tool',name));
  if(!(o.tools || []).length) t.appendChild(el('span','note','none registered'));

  $('ver').textContent = 'titan · ' + o.model;
}

function shortPath(p){
  if(!p) return '—';
  const parts = p.split('/');
  return parts.length > 4 ? '…/' + parts.slice(-3).join('/') : p;
}

load();
setInterval(load, 10000);
</script>
</body>
</html>`
