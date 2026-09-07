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
.bar .mark{width:24px;height:24px;flex:none}
.bar b{font-size:15px;font-weight:650;letter-spacing:-.01em}
.bar .sub{font-family:var(--mono);font-size:10.5px;color:var(--muted)}
.bar .spacer{flex:1}
.bar .who{font-family:var(--mono);font-size:11px;color:var(--ink-2);
  background:var(--sunken);border:1px solid var(--line);border-radius:11px;
  padding:2px 9px}

/* ---------------------------------------------------------------- hero */
.hero{padding:46px 0 38px;display:grid;grid-template-columns:minmax(0,1fr) auto;
  gap:56px;align-items:center}
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

/* Sign-in form. Shown only when Titan holds the accounts; an OIDC
   deployment gets a redirect button instead, because the password never
   belongs to Titan in that mode. */
.signin{background:var(--surface);border:1px solid var(--line);border-radius:11px;
  padding:20px 22px;max-width:360px;box-shadow:0 2px 14px -8px rgba(0,0,0,.3)}
.signin label{display:block;font-family:var(--mono);font-size:10px;
  letter-spacing:.09em;text-transform:uppercase;color:var(--muted);margin-bottom:5px}
.signin input{width:100%;background:var(--sunken);border:1px solid var(--line);
  border-radius:7px;padding:9px 11px;font-size:14px;color:var(--ink);margin-bottom:13px}
.signin input:focus{outline:none;border-color:var(--accent);
  box-shadow:0 0 0 3px var(--accent-soft)}
.signin .btn{width:100%;justify-content:center}
.err{background:var(--warn-bg);color:var(--warn);border-radius:6px;padding:8px 11px;
  font-size:12.5px;margin-bottom:12px}
.alt{display:flex;align-items:center;gap:10px;margin:16px 0 14px;
  font-family:var(--mono);font-size:10px;color:var(--muted)}
.alt::before,.alt::after{content:"";flex:1;height:1px;background:var(--line)}
.oauth{display:flex;flex-direction:column;gap:8px}
.oauth a{display:flex;align-items:center;justify-content:center;gap:9px;
  background:var(--surface);border:1px solid var(--line);border-radius:8px;
  padding:9px 14px;font-size:13.5px;font-weight:520;text-decoration:none;
  color:var(--ink);transition:border-color .14s}
.oauth a:hover{border-color:var(--accent)}
.oauth svg{width:16px;height:16px;flex:none}

/* The column, drawn large. Same three strokes as the CLI banner. */
/* The hero visual: a live network with the mark sitting at its centre, so
   the emblem reads as the thing the signals converge on. */
.viz{position:relative;width:340px;height:250px;flex:none}
.viz canvas{position:absolute;inset:0;width:100%;height:100%}
.emblem{position:absolute;left:50%;top:50%;width:88px;height:88px;
  transform:translate(-50%,-50%);
  filter:drop-shadow(0 6px 22px rgba(31,111,184,.45))}

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

/* The thesis. A number this large is the argument, so it is set as a figure
   rather than buried in the prose — but it stays next to its source, because a
   statistic without provenance reads as marketing. */
.thesis{display:grid;grid-template-columns:auto minmax(0,1fr);gap:28px;
  align-items:start;padding:22px 24px;background:var(--surface);
  border:1px solid var(--line);border-radius:12px;margin-bottom:34px}
.thesis h2{margin-top:0}
.thesis p{margin:0 0 9px;max-width:64ch}
.thesis p:last-child{margin-bottom:0}
.thesis .muted{color:var(--muted);font-size:13px}
.figure{text-align:center;padding-right:26px;border-right:1px solid var(--line);
  min-width:132px}
.figure b{display:block;font-size:38px;font-weight:680;letter-spacing:-.03em;
  color:var(--accent);line-height:1}
.figure span{display:block;margin-top:5px;font-family:var(--mono);font-size:10px;
  letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}
@media (max-width:700px){
  .thesis{grid-template-columns:1fr;gap:18px}
  .figure{text-align:left;padding:0 0 16px;border-right:0;
    border-bottom:1px solid var(--line)}
}

/* Containment. Each row is a boundary that either holds or does not, so the
   mark carries the state and the text says what it means in practice — a green
   tick with no explanation is decoration, not information. */
.lede{margin:-6px 0 16px;color:var(--muted);max-width:62ch}
.posture{list-style:none;margin:0;padding:0;display:grid;gap:9px}
.posture li{display:grid;grid-template-columns:18px 1fr;gap:11px;
  align-items:start;background:var(--surface);border:1px solid var(--line);
  border-radius:9px;padding:11px 13px}
.posture .mk{font-family:var(--mono);font-size:13px;line-height:1.35;font-weight:700}
.posture .yes .mk{color:var(--ok)}
.posture .no .mk{color:var(--warn)}
.posture b{display:block;font-size:13px;font-weight:600;letter-spacing:-.005em}
.posture span{display:block;color:var(--muted);font-size:12.5px;margin-top:1px}

footer{border-top:1px solid var(--line);padding:20px 0 30px;
  font-family:var(--mono);font-size:10.5px;color:var(--muted);
  display:flex;gap:18px;flex-wrap:wrap}

@media (max-width:760px){
  .hero{grid-template-columns:1fr}
  .viz{width:100%;height:190px}
}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
</style>
</head>
<body>

<header>
  <div class="wrap bar">
    <svg class="mark" viewBox="0 0 256 256" aria-hidden="true">
      <defs><linearGradient id="tgh" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0%" stop-color="#3FA9F5"/>
        <stop offset="55%" stop-color="#1F6FB8"/>
        <stop offset="100%" stop-color="#123E6B"/>
      </linearGradient></defs>
      <path d="M128 8 236 70v116L128 248 20 186V70Z" fill="url(#tgh)"/>
      <g fill="#fff">
        <rect x="66" y="74" width="124" height="26" rx="5"/>
        <rect x="114" y="100" width="28" height="72"/>
        <rect x="80" y="172" width="96" height="24" rx="5"/>
      </g>
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
      <h1 id="headline">A coding agent for code that cannot leave the building.</h1>
      <p id="pitch">Titan runs on hardware you own, against a model you host.
        Air-gap capable, sandboxed, and every action recorded — because the
        teams who need an agent most are the ones who cannot send their source
        to an API.</p>
      <div class="cta" id="cta"></div>
    </div>
    <div class="viz">
      <canvas id="net" aria-hidden="true"></canvas>
      <svg class="emblem" viewBox="0 0 256 256" aria-hidden="true">
        <defs><linearGradient id="tge" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stop-color="#3FA9F5"/>
          <stop offset="55%" stop-color="#1F6FB8"/>
          <stop offset="100%" stop-color="#123E6B"/>
        </linearGradient></defs>
        <path d="M128 8 236 70v116L128 248 20 186V70Z" fill="url(#tge)"/>
        <g fill="#fff">
          <rect x="66" y="74" width="124" height="26" rx="5"/>
          <rect x="114" y="100" width="28" height="72"/>
          <rect x="80" y="172" width="96" height="24" rx="5"/>
        </g>
      </svg>
    </div>
  </div>

  <section class="thesis">
    <div class="figure"><b>7.80&times;</b><span>harness &gt; model variance</span></div>
    <div>
      <h2>Why the scaffold is the product</h2>
      <p>A controlled study on SWE-bench Verified found harness-induced variance
        exceeds model-induced variance by <b>7.80&times;</b>, reversing the
        ranking in 6 of 9 model-pair comparisons. The context management, tool
        design, and permission model around the model decide more of the outcome
        than the model does.</p>
      <p class="muted">Most agents are a wrapper around someone else's API, with
        the scaffold treated as glue. Titan engineers and evaluates that layer
        separately, behind a provider abstraction. Better model, better agent.
        Better harness, better agent. Both compound.</p>
    </div>
  </section>

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

  <section>
    <h2>Containment</h2>
    <p class="lede">This agent runs shell commands and edits files. What stops it
      mattering is not that it is trusted — it is what it cannot reach.</p>
    <ul class="posture" id="posture"></ul>
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
  //
  // The CTA is rebuilt only when the signed-in state actually CHANGES, never
  // on a routine poll. Rebuilding it every ten seconds destroyed the card the
  // user was typing into: a half-filled sign-up form was replaced with a fresh
  // sign-in form mid-keystroke.
  const cta = $('cta');
  const state = [o.authenticated, o.local_auth, o.allow_signup,
                 o.sign_in_url || ''].join('|');
  if(cta.dataset.state === state){
    renderFacts(o);
    return;
  }
  cta.dataset.state = state;
  cta.textContent = '';

  if(o.local_auth && !o.authenticated){
    // Titan holds the accounts: render a real form.
    $('headline').textContent = 'Sign in to Titan';
    $('pitch').textContent =
      'Your session is scoped to your tenant, and every action the agent takes ' +
      'is recorded and replayable.';
    cta.appendChild(signInForm(o));
  }else if(o.sign_in_url && !o.authenticated){
    // A configured deployment with no local accounts: the whole front door
    // is the provider button.
    $('headline').textContent = 'Sign in to Titan';
    $('pitch').textContent =
      'This deployment uses your organisation’s identity provider. ' +
      'Sessions are scoped to your tenant and every action is recorded.';
    const box = el('div','signin');
    box.appendChild(oauthButtons(o));
    cta.appendChild(box);
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

  renderFacts(o);
}

// renderFacts paints everything below the hero. Split out of load() so the
// ten-second refresh can update the live numbers without touching a sign-in
// or sign-up card the user is part-way through filling in.
function renderFacts(o){
  // ---- what this deployment actually is
  const f = $('facts');
  f.textContent = '';
  f.appendChild(card('Model', o.model,
    o.context_window ? o.context_window.toLocaleString() + ' token context' : ''));
  // The workspace path is withheld from anonymous visitors: it names the
  // operator's account and directory layout. Saying so is more honest than
  // rendering an empty card, and it demonstrates the restraint rather than
  // just claiming it.
  f.appendChild(o.workspace
    ? card('Workspace', shortPath(o.workspace), 'the only path the agent can reach')
    : card('Workspace', 'hidden', 'shown once you sign in', 'on'));
  // Containerised deployments report tier "none" because the boundary is the
  // container around the whole process, not a sandbox inside it. Flagging that
  // as a warning would be exactly backwards, so it is read from the deployment
  // rather than inferred from the tier string alone.
  f.appendChild(card('Isolation', o.isolation || o.sandbox,
    o.sandbox_network ? 'network allowed' : 'no network access',
    o.isolation_ok ? 'on' : (o.sandbox === 'none' ? 'warn' : 'on')));
  f.appendChild(card('Storage', o.durable ? 'postgres' : 'in-memory',
    o.storage, o.durable ? 'on' : 'off'));
  f.appendChild(card('Authentication', authValue(o), authDetail(o),
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

  // ---- containment
  //
  // Every row is read from the running deployment, never hardcoded: a claim
  // about isolation that is not checked against reality is worse than no claim,
  // because it is believed. A boundary that does NOT hold is shown as plainly
  // as one that does.
  const p = $('posture');
  p.textContent = '';
  const rows = [
    [o.isolation_ok,
     o.isolation === 'container'
       ? 'The agent runs inside a container'
       : 'Execution sandbox: ' + (o.isolation || 'none'),
     o.isolation === 'container'
       ? 'It has no path to the host filesystem — not restricted, absent. ' +
         'The only writable surface is a throwaway volume.'
       : 'Tool execution is confined to the ' + (o.isolation || 'none') + ' tier.'],
    [!o.sandbox_network,
     o.sandbox_network ? 'The agent can reach the network' : 'No network egress',
     o.sandbox_network
       ? 'A prompt injection has somewhere to send what it finds.'
       : 'Nothing the agent reads can be sent anywhere.'],
    [o.auth_mode !== 'none',
     o.auth_mode === 'none' ? 'Anyone can use this deployment' : 'Sign-in required',
     o.auth_mode === 'none'
       ? 'No account is needed to drive the agent.'
       : 'Every session belongs to one account and is visible only to it.'],
    [!o.allow_signup,
     o.allow_signup ? 'Anyone can create an account' : 'Accounts are issued, not self-served',
     o.allow_signup
       ? 'Registration is open to the public internet.'
       : 'Only the operator can add a user.'],
    [true, 'Every action is recorded',
     'Tool calls, approvals and results are appended to a replayable log.'],
    [true, 'Tool output is data, never instructions',
     'Everything the agent reads is tagged untrusted at ingest.'],
  ];
  for(const [ok, title, detail] of rows){
    const li = el('li', ok ? 'yes' : 'no');
    li.appendChild(el('span','mk', ok ? '✓' : '!'));
    const body = el('div');
    body.appendChild(el('b','',title));
    body.appendChild(el('span','',detail));
    li.appendChild(body);
    p.appendChild(li);
  }

  $('ver').textContent = 'titan · ' + o.model;
}

// signInForm builds the username/password card.
function signInForm(o){
  const box = el('div','signin');

  const err = el('div','err');
  err.hidden = true;
  box.appendChild(err);

  box.appendChild(el('label',null,'Username or email'));
  const user = document.createElement('input');
  user.type = 'text'; user.autocomplete = 'username'; user.autofocus = true;
  box.appendChild(user);

  box.appendChild(el('label',null,'Password'));
  const pass = document.createElement('input');
  pass.type = 'password'; pass.autocomplete = 'current-password';
  box.appendChild(pass);

  const go = el('button','btn','Sign in');
  box.appendChild(go);

  async function submit(){
    err.hidden = true;
    if(!user.value || !pass.value){
      err.textContent = 'Enter a username and password.';
      err.hidden = false;
      return;
    }
    go.disabled = true; go.textContent = 'Signing in…';
    try{
      const r = await fetch('/v1/signin', {
        method:'POST', headers:{'Content-Type':'application/json'},
        body: JSON.stringify({username:user.value, password:pass.value}),
      });
      const body = await r.json();
      if(!r.ok){
        err.textContent = body.error || 'Sign-in failed.';
        err.hidden = false;
        pass.value = ''; pass.focus();
        return;
      }
      // A password set by an administrator is temporary; say so rather than
      // letting it quietly become permanent.
      if(body.must_change_password){
        alert('This password was set for you. Change it from the console once signed in.');
      }
      location.href = '/console';
    }catch(e){
      err.textContent = 'Cannot reach the server.';
      err.hidden = false;
    }finally{
      go.disabled = false; go.textContent = 'Sign in';
    }
  }

  go.onclick = submit;
  for(const f of [user, pass]){
    f.addEventListener('keydown', e => { if(e.key === 'Enter') submit(); });
  }

  // A deployment can run both: password OR the corporate IdP.
  if(o.sign_in_url){
    box.appendChild(el('div','alt','or'));
    box.appendChild(oauthButtons(o));
  }

  const foot = el('div','note');
  foot.style.marginTop = '13px';
  if(o.allow_signup){
    foot.textContent = 'No account? ';
    const a = document.createElement('a');
    a.href = '#'; a.textContent = 'Create one';
    a.onclick = e => { e.preventDefault(); swapCard(box, signUpForm(o)); };
    foot.appendChild(a);
  }else{
    // Self-registration is off. Say what actually gets an account rather
    // than leaving the reader at a dead end.
    foot.textContent = 'Accounts are created by an administrator: titan user add <name>';
  }
  box.appendChild(foot);
  return box;
}

// Brand marks, drawn inline. No CDN: this page ships inside an air-gapped
// bundle, so an <img> pointing at a Google server would render as a broken
// icon exactly where trust matters most.
const MARKS = {
  Google: '<svg viewBox="0 0 24 24">' +
    '<path fill="#4285F4" d="M23.5 12.3c0-.8-.1-1.6-.2-2.3H12v4.5h6.5a5.6 5.6 0 0 1-2.4 3.7v3h3.9c2.3-2.1 3.5-5.2 3.5-8.9z"/>' +
    '<path fill="#34A853" d="M12 24c3.2 0 5.9-1.1 7.9-2.9l-3.9-3a7.2 7.2 0 0 1-10.7-3.8h-4v3.1A12 12 0 0 0 12 24z"/>' +
    '<path fill="#FBBC05" d="M5.3 14.3a7.1 7.1 0 0 1 0-4.6v-3.1h-4a12 12 0 0 0 0 10.8l4-3.1z"/>' +
    '<path fill="#EA4335" d="M12 4.8c1.8 0 3.4.6 4.6 1.8l3.4-3.4A12 12 0 0 0 1.3 6.6l4 3.1A7.2 7.2 0 0 1 12 4.8z"/></svg>',
  Microsoft: '<svg viewBox="0 0 24 24">' +
    '<path fill="#F25022" d="M2 2h9.4v9.4H2z"/><path fill="#7FBA00" d="M12.6 2H22v9.4h-9.4z"/>' +
    '<path fill="#00A4EF" d="M2 12.6h9.4V22H2z"/><path fill="#FFB900" d="M12.6 12.6H22V22h-9.4z"/></svg>',
  GitHub: '<svg viewBox="0 0 24 24"><path fill="currentColor" d="M12 .5A11.5 11.5 0 0 0 .5 12a11.5 11.5 0 0 0 7.9 10.9c.6.1.8-.2.8-.6v-2c-3.2.7-3.9-1.5-3.9-1.5-.5-1.3-1.3-1.7-1.3-1.7-1-.7.1-.7.1-.7 1.1.1 1.7 1.2 1.7 1.2 1 1.7 2.7 1.2 3.4.9.1-.7.4-1.2.7-1.5-2.6-.3-5.3-1.3-5.3-5.8 0-1.3.5-2.3 1.2-3.1-.1-.3-.5-1.5.1-3.2 0 0 1-.3 3.2 1.2a11 11 0 0 1 5.8 0c2.2-1.5 3.2-1.2 3.2-1.2.6 1.7.2 2.9.1 3.2.8.8 1.2 1.8 1.2 3.1 0 4.5-2.7 5.5-5.3 5.8.4.4.8 1.1.8 2.2v3.3c0 .4.2.7.8.6A11.5 11.5 0 0 0 23.5 12 11.5 11.5 0 0 0 12 .5z"/></svg>',
};

// oauthButtons renders one button per configured identity provider.
function oauthButtons(o){
  const wrap = el('div','oauth');
  const label = o.provider_label || 'SSO';
  const a = document.createElement('a');
  a.href = o.sign_in_url;
  a.innerHTML = (MARKS[label] || '') + '<span></span>';
  a.querySelector('span').textContent = 'Continue with ' + label;
  wrap.appendChild(a);
  return wrap;
}

// ---------------------------------------------------------------- hero viz
//
// A layered network with signals propagating left to right. It is decoration,
// but honest decoration: the shape is a real feed-forward topology, and the
// pulses travel along actual edges rather than being random sparkle.
//
// Drawn on a canvas rather than as animated SVG because a few hundred moving
// elements in the DOM costs far more than one repainted bitmap, and this page
// is the first thing a browser loads.
function startViz(){
  const c = document.getElementById('net');
  if(!c) return;
  const ctx = c.getContext('2d');
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;

  // Layer sizes chosen so the silhouette widens then narrows: it reads as
  // "many inputs, one considered answer", which is what the agent does.
  const LAYERS = [5, 8, 8, 5];
  let nodes = [], edges = [], pulses = [], W = 0, H = 0;

  function accent(){
    return getComputedStyle(document.documentElement)
      .getPropertyValue('--accent').trim() || '#1F6FB8';
  }

  function layout(){
    const r = c.getBoundingClientRect();
    const dpr = Math.min(devicePixelRatio || 1, 2);
    W = r.width; H = r.height;
    c.width = W * dpr; c.height = H * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    nodes = []; edges = [];
    const padX = 22, padY = 20;
    LAYERS.forEach((count, li) => {
      const x = padX + (W - 2*padX) * (li / (LAYERS.length - 1));
      for(let i = 0; i < count; i++){
        const y = count === 1 ? H/2
          : padY + (H - 2*padY) * (i / (count - 1));
        nodes.push({x, y, layer: li, phase: Math.random() * Math.PI * 2});
      }
    });

    // Connect each layer to the next, skipping the edges that would pass
    // straight through the emblem in the middle.
    const cx = W/2, cy = H/2, keepOut = 52;
    for(let li = 0; li < LAYERS.length - 1; li++){
      const a = nodes.filter(n => n.layer === li);
      const b = nodes.filter(n => n.layer === li + 1);
      for(const p of a) for(const q of b){
        const mx = (p.x + q.x)/2, my = (p.y + q.y)/2;
        if(Math.hypot(mx - cx, my - cy) < keepOut) continue;
        edges.push({a: p, b: q});
      }
    }
  }

  function spawn(){
    if(!edges.length) return;
    pulses.push({e: edges[(Math.random() * edges.length) | 0], t: 0,
                 v: 0.006 + Math.random() * 0.010});
  }

  let last = 0;
  function frame(now){
    const dt = Math.min((now - last) || 16, 50); last = now;
    ctx.clearRect(0, 0, W, H);
    const col = accent();

    // Edges, faint.
    ctx.strokeStyle = col; ctx.globalAlpha = 0.10; ctx.lineWidth = 1;
    ctx.beginPath();
    for(const e of edges){
      ctx.moveTo(e.a.x, e.a.y);
      ctx.lineTo(e.b.x, e.b.y);
    }
    ctx.stroke();

    // Pulses travelling along them.
    for(const p of pulses){
      p.t += p.v * (dt / 16);
      const x = p.e.a.x + (p.e.b.x - p.e.a.x) * p.t;
      const y = p.e.a.y + (p.e.b.y - p.e.a.y) * p.t;
      const fade = Math.sin(Math.PI * Math.min(p.t, 1));
      ctx.globalAlpha = 0.75 * fade;
      ctx.fillStyle = col;
      ctx.beginPath(); ctx.arc(x, y, 2.1, 0, 7); ctx.fill();
    }
    pulses = pulses.filter(p => p.t < 1);
    if(!reduce && pulses.length < 34 && Math.random() < 0.5) spawn();

    // Nodes on top, breathing slightly so a still frame still reads as alive.
    for(const n of nodes){
      const b = reduce ? 0.55 : 0.45 + 0.25 * Math.sin(now/900 + n.phase);
      ctx.globalAlpha = b;
      ctx.fillStyle = col;
      ctx.beginPath(); ctx.arc(n.x, n.y, 3.4, 0, 7); ctx.fill();
      ctx.globalAlpha = b * 0.25;
      ctx.beginPath(); ctx.arc(n.x, n.y, 7.5, 0, 7); ctx.fill();
    }
    ctx.globalAlpha = 1;
    requestAnimationFrame(frame);
  }

  layout();
  addEventListener('resize', layout);
  // Seed a few pulses so the very first painted frame is not an empty grid.
  for(let i = 0; i < 12; i++){ spawn(); pulses[pulses.length-1].t = Math.random(); }
  requestAnimationFrame(frame);
}

// swapCard exchanges one card for another in place, so sign-in and sign-up
// share a position on the page instead of navigating.
function swapCard(from, to){
  from.replaceWith(to);
  const first = to.querySelector('input');
  if(first) first.focus();
}

// signUpForm is self-registration, shown only when the deployment enables it.
function signUpForm(o){
  const box = el('div','signin');

  const h = el('div','note','Create an account');
  h.style.cssText = 'font-size:13px;color:var(--ink);margin-bottom:14px;font-weight:600';
  box.appendChild(h);

  const err = el('div','err'); err.hidden = true;
  box.appendChild(err);

  function field(label, type, auto){
    box.appendChild(el('label', null, label));
    const i = document.createElement('input');
    i.type = type; i.autocomplete = auto;
    box.appendChild(i);
    return i;
  }
  const user = field('Username', 'text', 'username');
  const email = field('Email', 'email', 'email');
  const name = field('Display name (optional)', 'text', 'name');
  const pass = field('Password', 'password', 'new-password');
  const conf = field('Confirm password', 'password', 'new-password');

  const hint = el('div','note','At least 10 characters.');
  hint.style.margin = '-6px 0 13px';
  box.appendChild(hint);

  const go = el('button','btn','Create account');
  box.appendChild(go);

  async function submit(){
    err.hidden = true;
    const fail = m => { err.textContent = m; err.hidden = false; };
    if(!user.value || !pass.value) return fail('Username and password are required.');
    // Checked here as well as on the server: catching it in the browser
    // saves a round trip, but the server is what actually enforces it.
    if(pass.value.length < 10)   return fail('Password must be at least 10 characters.');
    if(pass.value !== conf.value) return fail('The two passwords do not match.');

    go.disabled = true; go.textContent = 'Creating…';
    try{
      const r = await fetch('/v1/signup', {
        method:'POST', headers:{'Content-Type':'application/json'},
        body: JSON.stringify({username:user.value, email:email.value,
                              name:name.value, password:pass.value}),
      });
      const body = await r.json();
      if(!r.ok) return fail(body.error || 'Could not create the account.');

      // Created: sign straight in rather than making them retype it.
      const si = await fetch('/v1/signin', {
        method:'POST', headers:{'Content-Type':'application/json'},
        body: JSON.stringify({username:user.value, password:pass.value}),
      });
      if(si.ok){ location.href = '/console'; return; }
      fail('Account created — please sign in.');
    }catch(e){
      fail('Cannot reach the server.');
    }finally{
      go.disabled = false; go.textContent = 'Create account';
    }
  }
  go.onclick = submit;
  for(const f of [user, email, name, pass, conf]){
    f.addEventListener('keydown', e => { if(e.key === 'Enter') submit(); });
  }

  const back = el('div','note');
  back.style.marginTop = '13px';
  back.textContent = 'Already have an account? ';
  const a = document.createElement('a');
  a.href = '#'; a.textContent = 'Sign in';
  a.onclick = e => { e.preventDefault(); swapCard(box, signInForm(o)); };
  back.appendChild(a);
  box.appendChild(back);

  return box;
}

// authValue and authDetail describe how this deployment identifies people.
// One label per mode: saying "identity provider configured" for local accounts
// was simply untrue, and the front door is the wrong place to be vague.
function authValue(o){
  if(o.auth_mode === 'local'){
    return o.sign_in_url ? 'local + ' + (o.provider_label || 'SSO') : 'local accounts';
  }
  if(o.auth_mode === 'oidc') return o.provider_label || 'oidc';
  return o.auth_mode;
}
function authDetail(o){
  switch(o.auth_mode){
    case 'none':  return 'single tenant, no accounts';
    case 'local': return o.allow_signup
      ? 'password sign-in, open registration'
      : 'password sign-in, accounts created by an administrator';
    case 'proxy': return 'identity asserted by a trusted proxy';
    default:      return 'sessions verified against your identity provider';
  }
}

function shortPath(p){
  if(!p) return '—';
  const parts = p.split('/');
  return parts.length > 4 ? '…/' + parts.slice(-3).join('/') : p;
}

load();
startViz();
setInterval(load, 10000);
</script>
</body>
</html>`
