package server

import (
	"net/http"
	"strings"
)

// serveConsole serves the web console.
//
// One self-contained HTML document with no external requests: an air-gapped
// enclave has no CDN, and a build step would mean the binary alone is not a
// complete install. Everything here is hand-written CSS and vanilla JS against
// the same SSE stream the CLI consumes.
//
// The layout is an operator console, not a chat window: a session rail, the
// transcript, and a run inspector carrying the numbers that matter on owned
// GPUs — turns, tokens, cache hit rate, tool tallies, terminal reason.
func (s *Server) serveConsole(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
	w.Write([]byte(consoleHTML))
}

var consoleHTML = strings.ReplaceAll(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Titan Console</title>
<style>
:root{
  --bg:#F5F7FA; --surface:#FFFFFF; --raised:#FFFFFF; --sunken:#EDF1F6;
  --line:#DCE3EC; --line-strong:#C4CFDD;
  --ink:#0F141B; --ink-2:#3A4757; --muted:#697786;
  --accent:#1F6FB8; --accent-soft:#E3EEF8; --accent-line:#1F6FB8;
  --running:#1F6FB8; --done:#1A7F4B; --waiting:#9A6A16; --error:#C0392F;
  --running-bg:#E3EEF8; --done-bg:#E3F3EA; --waiting-bg:#FAF0DC; --error-bg:#FBE9E7;
  --mono:ui-monospace,SFMono-Regular,"SF Mono",Menlo,Consolas,"Liberation Mono",monospace;
  --sans:-apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,Roboto,sans-serif;
  --rail:296px; --inspector:304px;
}
@media (prefers-color-scheme:dark){
  :root:not([data-theme="light"]){
    --bg:#0B0E13; --surface:#141922; --raised:#1B212C; --sunken:#0F141C;
    --line:#252D3A; --line-strong:#333D4D;
    --ink:#E8EDF4; --ink-2:#BAC6D4; --muted:#8A96A8;
    --accent:#4C8FD6; --accent-soft:#16283C; --accent-line:#4C8FD6;
    --running:#4C8FD6; --done:#3FAF6C; --waiting:#D4A03C; --error:#E05A52;
    --running-bg:#132436; --done-bg:#0F2419; --waiting-bg:#241C0C; --error-bg:#2A1412;
  }
}
:root[data-theme="dark"]{
  --bg:#0B0E13; --surface:#141922; --raised:#1B212C; --sunken:#0F141C;
  --line:#252D3A; --line-strong:#333D4D;
  --ink:#E8EDF4; --ink-2:#BAC6D4; --muted:#8A96A8;
  --accent:#4C8FD6; --accent-soft:#16283C; --accent-line:#4C8FD6;
  --running:#4C8FD6; --done:#3FAF6C; --waiting:#D4A03C; --error:#E05A52;
  --running-bg:#132436; --done-bg:#0F2419; --waiting-bg:#241C0C; --error-bg:#2A1412;
}

*{box-sizing:border-box}
html,body{height:100%}
body{margin:0;background:var(--bg);color:var(--ink);font-family:var(--sans);
  font-size:13.5px;line-height:1.55;-webkit-font-smoothing:antialiased;overflow:hidden}
button,select,textarea,input{font:inherit;color:inherit}
:focus-visible{outline:2px solid var(--accent);outline-offset:1px;border-radius:3px}

/* ---------------------------------------------------------------- chrome */
.top{height:46px;display:flex;align-items:center;gap:14px;padding:0 16px;
  background:var(--surface);border-bottom:1px solid var(--line);flex:none}
.brand{display:flex;align-items:baseline;gap:8px}
.brand b{font-size:14px;font-weight:650;letter-spacing:-.01em}
.brand span{font-family:var(--mono);font-size:10.5px;color:var(--muted)}
.top .spacer{flex:1}
.stat{font-family:var(--mono);font-size:11px;color:var(--muted);display:flex;
  align-items:center;gap:6px;white-space:nowrap}
.stat b{color:var(--ink-2);font-weight:500}
.led{width:7px;height:7px;border-radius:50%;background:var(--muted);flex:none}
.led.up{background:var(--done);box-shadow:0 0 0 3px var(--done-bg)}
.led.down{background:var(--error);box-shadow:0 0 0 3px var(--error-bg)}
.who-chip{display:inline-flex;align-items:center;gap:6px;padding:2px 8px;
  border-radius:11px;background:var(--sunken);border:1px solid var(--line);
  font-family:var(--mono);font-size:10.5px;color:var(--ink-2)}
.who-chip::before{content:"";width:5px;height:5px;border-radius:50%;
  background:var(--done);flex:none}
.top a.ghost{text-decoration:none;line-height:1.6}

/* ---------------------------------------------------------------- shell */
.shell{display:grid;grid-template-columns:var(--rail) minmax(0,1fr) var(--inspector);
  height:calc(100vh - 46px)}
.rail{background:var(--surface);border-right:1px solid var(--line);
  display:flex;flex-direction:column;min-height:0}
.stage{display:flex;flex-direction:column;min-height:0;background:var(--bg)}
.inspector{background:var(--surface);border-left:1px solid var(--line);
  overflow-y:auto;min-height:0}

/* ---------------------------------------------------------------- composer */
.composer{padding:12px;border-bottom:1px solid var(--line);flex:none}
textarea{width:100%;min-height:70px;max-height:180px;resize:vertical;
  background:var(--sunken);border:1px solid var(--line);border-radius:6px;
  padding:9px 10px;font-size:13px;line-height:1.5}
textarea::placeholder{color:var(--muted)}
textarea:focus{outline:none;border-color:var(--accent);
  box-shadow:0 0 0 3px var(--accent-soft)}
.composer .row{display:flex;gap:8px;margin-top:8px;align-items:stretch}
select{background:var(--sunken);border:1px solid var(--line);border-radius:6px;
  padding:0 26px 0 9px;font-size:12px;font-family:var(--mono);cursor:pointer;
  appearance:none;background-image:linear-gradient(45deg,transparent 50%,currentColor 50%),
    linear-gradient(135deg,currentColor 50%,transparent 50%);
  background-position:calc(100% - 14px) 52%,calc(100% - 9px) 52%;
  background-size:5px 5px,5px 5px;background-repeat:no-repeat}
.go{flex:1;background:var(--accent);border:1px solid var(--accent);color:#fff;
  border-radius:6px;padding:7px 12px;font-size:12.5px;font-weight:600;cursor:pointer}
.go:hover:not(:disabled){filter:brightness(1.08)}
.go:disabled{opacity:.45;cursor:not-allowed}
.hint{font-family:var(--mono);font-size:10px;color:var(--muted);margin-top:7px}

/* ---------------------------------------------------------------- rail */
.rail-head{display:flex;align-items:center;justify-content:space-between;
  padding:10px 14px 6px;flex:none}
.rail-head span{font-family:var(--mono);font-size:10px;letter-spacing:.1em;
  text-transform:uppercase;color:var(--muted)}
.list{flex:1;overflow-y:auto;min-height:0}
.item{width:100%;text-align:left;background:none;border:0;border-bottom:1px solid var(--line);
  padding:10px 14px 10px 12px;cursor:pointer;display:block;border-left:2px solid transparent}
.item:hover{background:var(--sunken)}
.item[aria-current="true"]{background:var(--sunken);border-left-color:var(--accent)}
.item .q{font-size:12.5px;line-height:1.45;margin-bottom:5px;color:var(--ink);
  display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
.item .m{display:flex;align-items:center;gap:7px;font-family:var(--mono);
  font-size:10px;color:var(--muted)}
.pill{padding:1px 6px;border-radius:3px;font-size:9.5px;letter-spacing:.05em;
  text-transform:uppercase;font-weight:600;white-space:nowrap}
.pill.running{background:var(--running-bg);color:var(--running)}
.pill.completed,.pill.done{background:var(--done-bg);color:var(--done)}
.pill.waiting_approval{background:var(--waiting-bg);color:var(--waiting)}
.pill.error,.pill.max_turns,.pill.policy_denied,.pill.retry_exhausted,
.pill.max_budget,.pill.user_interrupt{background:var(--error-bg);color:var(--error)}

/* ---------------------------------------------------------------- stage */
.stage-head{height:38px;display:flex;align-items:center;gap:10px;padding:0 18px;
  border-bottom:1px solid var(--line);background:var(--surface);flex:none;
  font-family:var(--mono);font-size:11px;color:var(--muted)}
.stage-head .id{color:var(--ink-2)}
.stage-head .spacer{flex:1}
.ghost{background:none;border:1px solid var(--line);border-radius:5px;
  padding:3px 9px;font-family:var(--mono);font-size:10.5px;color:var(--ink-2);cursor:pointer}
.ghost:hover{background:var(--sunken);border-color:var(--line-strong)}
.transcript{flex:1;overflow-y:auto;padding:18px 22px 40px;min-height:0}
.empty{display:flex;flex-direction:column;align-items:center;justify-content:center;
  height:100%;gap:8px;color:var(--muted);text-align:center}
.empty .k{font-family:var(--mono);font-size:12px}
.empty .s{font-size:12px;max-width:34ch;line-height:1.6}

/* turn grouping: a vertical spine ties a turn's calls together */
.turn{position:relative;padding-left:20px;margin-bottom:4px}
.turn::before{content:"";position:absolute;left:5px;top:14px;bottom:2px;
  width:1px;background:var(--line)}
.turn:last-child::before{display:none}

.said{background:var(--raised);border:1px solid var(--line);border-radius:8px;
  padding:11px 14px;margin:0 0 12px;white-space:pre-wrap;line-height:1.6;
  overflow-wrap:anywhere}
.said.user{background:var(--accent-soft);border-color:var(--accent-line)}
.said .who{font-family:var(--mono);font-size:9.5px;letter-spacing:.1em;
  text-transform:uppercase;color:var(--muted);margin-bottom:6px}

.call{margin-bottom:10px}
.call .hdr{display:flex;align-items:baseline;gap:8px;font-family:var(--mono);
  font-size:11.5px;position:relative}
.call .hdr::before{content:"";position:absolute;left:-18px;top:6px;width:7px;height:7px;
  border-radius:50%;background:var(--accent);box-shadow:0 0 0 3px var(--bg)}
.call.err .hdr::before{background:var(--error)}
.call .tool{color:var(--accent);font-weight:600}
.call.err .tool{color:var(--error)}
.call .arg{color:var(--muted);overflow-wrap:anywhere}
.call .ms{margin-left:auto;color:var(--muted);font-size:10px;
  font-variant-numeric:tabular-nums;flex:none}
.out{font-family:var(--mono);font-size:11px;line-height:1.55;color:var(--ink-2);
  background:var(--sunken);border-left:2px solid var(--line-strong);
  border-radius:0 5px 5px 0;padding:8px 11px;margin-top:6px;white-space:pre-wrap;
  overflow-x:auto;max-height:300px;overflow-y:auto}
.out.err{border-left-color:var(--error);color:var(--error)}
.out.untrusted{border-left-color:var(--waiting)}
.tag{display:inline-block;font-family:var(--mono);font-size:9px;letter-spacing:.06em;
  text-transform:uppercase;padding:1px 5px;border-radius:3px;margin-bottom:5px;
  background:var(--waiting-bg);color:var(--waiting)}

.approve{border:1px solid var(--waiting);background:var(--waiting-bg);
  border-radius:8px;padding:13px 15px;margin:12px 0}
.approve h4{margin:0 0 4px;font-family:var(--mono);font-size:11.5px;color:var(--waiting)}
.approve p{margin:0 0 9px;font-size:12px;color:var(--ink-2)}
.approve pre{font-family:var(--mono);font-size:11px;background:var(--sunken);
  border-radius:5px;padding:9px;overflow-x:auto;margin:0 0 10px;color:var(--ink-2)}
.approve .row{display:flex;gap:8px}
.approve button{border-radius:5px;padding:5px 13px;font-size:12px;
  font-weight:600;cursor:pointer;border:1px solid var(--line)}
.approve .yes{background:var(--accent);border-color:var(--accent);color:#fff}
.approve .no{background:var(--surface)}

.note{font-family:var(--mono);font-size:10.5px;color:var(--muted);
  border-top:1px dashed var(--line);padding-top:9px;margin:14px 0 4px;
  display:flex;gap:14px;flex-wrap:wrap}
.note b{color:var(--ink-2);font-weight:500;font-variant-numeric:tabular-nums}

/* ---------------------------------------------------------------- inspector */
.insp-sec{padding:13px 15px;border-bottom:1px solid var(--line)}
.insp-sec h3{margin:0 0 9px;font-family:var(--mono);font-size:9.5px;letter-spacing:.11em;
  text-transform:uppercase;color:var(--muted);font-weight:600}
.kv{display:flex;justify-content:space-between;align-items:baseline;gap:10px;
  font-family:var(--mono);font-size:11.5px;padding:3px 0}
.kv dt{color:var(--muted)}
.kv dd{margin:0;color:var(--ink);font-variant-numeric:tabular-nums;text-align:right;
  overflow-wrap:anywhere}
.meter{height:4px;background:var(--sunken);border-radius:2px;overflow:hidden;margin-top:7px}
.meter i{display:block;height:100%;background:var(--accent);border-radius:2px}
.tally{display:flex;justify-content:space-between;font-family:var(--mono);
  font-size:11.5px;padding:3px 0}
.tally span:first-child{color:var(--accent)}
.tally span:last-child{color:var(--ink);font-variant-numeric:tabular-nums}
.insp-empty{padding:22px 15px;font-size:12px;color:var(--muted);line-height:1.6}

@media (max-width:1180px){
  .shell{grid-template-columns:var(--rail) minmax(0,1fr)}
  .inspector{display:none}
}
@media (prefers-reduced-motion:reduce){*{transition:none!important;animation:none!important}}
</style>
</head>
<body>

<div class="top">
  <div class="brand"><b>Titan</b><span id="ver">console</span></div>
  <div class="stat"><span class="led" id="led"></span><span id="health">connecting</span></div>
  <div class="spacer"></div>
  <div class="stat">model <b id="mdl">—</b></div>
  <div class="stat">active <b id="active">0</b></div>
  <div class="stat" id="whobox" hidden>
    <span class="who-chip" id="who"></span>
    <a class="ghost" id="signout" href="/logout">Sign out</a>
  </div>
</div>

<div class="shell">
  <!-- session rail -->
  <aside class="rail">
    <div class="composer">
      <textarea id="q" placeholder="Ask a question, or describe a change…"></textarea>
      <div class="row">
        <select id="mode" title="Permission mode">
          <option value="default">default</option>
          <option value="plan">plan</option>
          <option value="accept-edits">accept-edits</option>
          <option value="auto">auto</option>
        </select>
        <button class="go" id="go">Run</button>
      </div>
      <div class="hint">plan is read-only · ⌘↵ to run</div>
    </div>
    <div class="rail-head"><span>Sessions</span><span id="count"></span></div>
    <div class="list" id="list"></div>
  </aside>

  <!-- transcript -->
  <main class="stage">
    <div class="stage-head">
      <span class="id" id="sid">no session selected</span>
      <span class="spacer"></span>
      <button class="ghost" id="stop" hidden>Interrupt</button>
    </div>
    <div class="transcript" id="tx">
      <div class="empty">
        <div class="k">Titan</div>
        <div class="s">Ask a question or describe a change. Pick a session on the left to
          replay what it did.</div>
      </div>
    </div>
  </main>

  <!-- run inspector -->
  <aside class="inspector" id="insp">
    <div class="insp-empty">Run metrics appear here once a session is selected.</div>
  </aside>
</div>

<script>
"use strict";
const $ = id => document.getElementById(id);

let current = null;      // session id being viewed
let es = null;           // EventSource
let lastSeq = 0;         // highest seq rendered, for reconnect de-duplication
let live = false;        // is the viewed session still running
let turnEl = null;       // current turn container
const calls = new Map(); // call_id -> DOM node, to attach observations
const stats = {turns:0, tin:0, tout:0, cached:0, tools:{}, reason:null, compactions:0};

/* ------------------------------------------------------------------ api */
async function api(path, opts){
  const r = await fetch(path, {headers:{'Content-Type':'application/json'}, ...opts});
  if(!r.ok){
    let msg = r.statusText;
    try { msg = (await r.json()).error || msg; } catch {}
    throw new Error(msg);
  }
  return r.status === 204 ? null : r.json();
}

/* ------------------------------------------------------------------ chrome */
async function health(){
  try{
    const h = await api('/v1/health');
    $('led').className = 'led up';
    $('health').textContent = 'connected';
    $('mdl').textContent = h.model;
    $('active').textContent = h.sessions;
  }catch{
    $('led').className = 'led down';
    $('health').textContent = 'unreachable';
  }
}

async function refresh(){
  try{
    const list = await api('/v1/sessions');
    list.sort((a,b) => new Date(b.created) - new Date(a.created));
    $('count').textContent = list.length;

    const el = $('list');
    el.textContent = '';
    for(const s of list){
      const b = document.createElement('button');
      b.className = 'item';
      b.type = 'button';
      if(s.id === current) b.setAttribute('aria-current','true');

      const q = document.createElement('div');
      q.className = 'q';
      q.textContent = s.prompt || '(no prompt recorded)';

      const m = document.createElement('div');
      m.className = 'm';
      const pill = document.createElement('span');
      pill.className = 'pill ' + s.state;
      pill.textContent = s.state.replace(/_/g,' ');
      const when = document.createElement('span');
      when.textContent = ago(s.created);
      m.append(pill, when);

      b.append(q, m);
      b.onclick = () => openSession(s.id);
      el.appendChild(b);
    }
  }catch{}
}

function ago(iso){
  const s = Math.max(0, (Date.now() - new Date(iso)) / 1000);
  if(s < 60) return Math.floor(s) + 's ago';
  if(s < 3600) return Math.floor(s/60) + 'm ago';
  if(s < 86400) return Math.floor(s/3600) + 'h ago';
  return Math.floor(s/86400) + 'd ago';
}

/* ------------------------------------------------------------------ session */
function openSession(id){
  if(es){ es.close(); es = null; }
  current = id; lastSeq = 0; live = true; turnEl = null;
  calls.clear();
  Object.assign(stats, {turns:0, tin:0, tout:0, cached:0, tools:{}, reason:null, compactions:0});

  $('tx').textContent = '';
  $('sid').textContent = id;
  $('stop').hidden = false;
  drawInspector();
  refresh();
  connect(id);
}

// connect opens the event stream and keeps it open.
//
// EventSource fires onerror on ANY interruption, including the normal close at
// the end of a stream. Reconnecting is what makes a long session survive a
// dropped connection; de-duplicating by seq is what stops the replayed backlog
// from rendering twice.
function connect(id){
  es = new EventSource('/v1/sessions/' + id + '/events');

  es.onmessage = e => {
    let ev; try{ ev = JSON.parse(e.data); }catch{ return; }
    if(ev.seq <= lastSeq) return;
    lastSeq = ev.seq;
    render(ev);
    drawInspector();
  };

  es.onerror = () => {
    if(!es) return;
    es.close(); es = null;
    // A finished session's stream closes normally once the backlog is sent.
    // Only a live session is worth reconnecting to.
    if(live && current === id){
      setTimeout(() => { if(current === id && !es) connect(id); }, 1500);
    }
  };
}

/* ------------------------------------------------------------------ render */
function node(cls, text){
  const d = document.createElement('div');
  if(cls) d.className = cls;
  if(text !== undefined) d.textContent = text;
  return d;
}

function newTurn(){
  turnEl = node('turn');
  $('tx').appendChild(turnEl);
  return turnEl;
}

function render(ev){
  const tx = $('tx');
  const p = ev.payload || {};

  switch(ev.type){
    case 'user.message': {
      const b = node('said user');
      b.append(node('who','you'), document.createTextNode(p.text || ''));
      tx.appendChild(b);
      newTurn();
      stats.turns++;
      break;
    }

    case 'agent.message': {
      if(!(p.text || '').trim()) break;
      const b = node('said');
      b.append(node('who','titan'), document.createTextNode(p.text));
      (turnEl || tx).appendChild(b);
      break;
    }

    case 'action.requested': {
      const wrap = node('call');
      const hdr = node('hdr');
      const tool = node('tool', p.tool);
      const arg = node('arg', summarize(p.tool, p.args));
      hdr.append(tool, arg);
      wrap.appendChild(hdr);
      (turnEl || newTurn()).appendChild(wrap);
      if(p.call_id) calls.set(p.call_id, wrap);
      stats.tools[p.tool] = (stats.tools[p.tool] || 0) + 1;
      if(p.requires_approval) approval(p);
      break;
    }

    case 'observation': {
      const wrap = calls.get(p.call_id) || turnEl || newTurn();
      if(p.is_error) wrap.classList.add('err');

      let cls = 'out';
      if(p.is_error) cls += ' err';
      else if(ev.trust === 'untrusted') cls += ' untrusted';

      const body = node(cls);
      if(ev.trust === 'untrusted' && !p.is_error){
        // Provenance is a first-class concept in Titan: tool output is data,
        // never instruction. Saying so in the UI keeps that visible.
        body.appendChild(node('tag','untrusted data'));
      }
      body.appendChild(document.createTextNode(clip(p.content || '', 4000)));
      wrap.appendChild(body);

      if(p.duration_ms !== undefined){
        const hdr = wrap.querySelector('.hdr');
        if(hdr && !hdr.querySelector('.ms')){
          hdr.appendChild(node('ms', p.duration_ms + 'ms'));
        }
      }
      break;
    }

    case 'action.denied': {
      const wrap = calls.get(p.call_id) || turnEl || newTurn();
      wrap.classList.add('err');
      wrap.appendChild(node('out err', 'denied — ' + (p.reason || 'no reason given')));
      break;
    }

    case 'compaction.completed': {
      stats.compactions++;
      tx.appendChild(node('note',
        'context compacted · ' + p.before_tokens + ' → ' + p.after_tokens + ' tokens'));
      turnEl = null;
      break;
    }

    case 'session.ended': {
      live = false;
      $('stop').hidden = true;
      stats.reason = p.reason;
      stats.turns = p.turns || stats.turns;
      stats.tin = p.tokens_in || stats.tin;
      stats.tout = p.tokens_out || stats.tout;
      stats.cached = p.tokens_cached || stats.cached;
      stats.compactions = p.compactions || stats.compactions;

      const n = node('note');
      n.append(kv('ended', p.reason), kv('turns', p.turns),
               kv('tokens', (p.tokens_in||0).toLocaleString() + ' in / ' +
                            (p.tokens_out||0).toLocaleString() + ' out'));
      tx.appendChild(n);
      refresh();
      break;
    }
  }
  tx.scrollTop = tx.scrollHeight;
}

function kv(k, v){
  const s = document.createElement('span');
  s.append(document.createTextNode(k + ' '), Object.assign(document.createElement('b'),
    {textContent: String(v)}));
  return s;
}

function summarize(tool, args){
  if(!args) return '';
  let a = args;
  if(typeof a === 'string'){ try{ a = JSON.parse(a); }catch{ return ''; } }
  switch(tool){
    case 'bash': return a.description || a.command || '';
    case 'read': case 'write': case 'edit': return shortPath(a.path || '');
    case 'glob': return a.pattern || '';
    case 'grep': return JSON.stringify(a.pattern || '');
    case 'search': return a.query || '';
    case 'task': return a.description || '';
    default: return a.path || a.pattern || a.query || a.description || '';
  }
}

function shortPath(p){
  const parts = String(p).split('/');
  return parts.length > 3 ? '…/' + parts.slice(-2).join('/') : p;
}

function clip(s, n){ return s.length > n ? s.slice(0,n) + '\n… ' + (s.length-n) + ' more characters' : s; }

/* ------------------------------------------------------------------ approvals */
function approval(p){
  const card = node('approve');
  const h = document.createElement('h4');
  h.textContent = 'Approval required — ' + p.tool;
  card.appendChild(h);
  if(p.reason) card.appendChild(Object.assign(document.createElement('p'), {textContent: p.reason}));

  const pre = document.createElement('pre');
  try{
    pre.textContent = JSON.stringify(
      typeof p.args === 'string' ? JSON.parse(p.args) : p.args, null, 2);
  }catch{ pre.textContent = String(p.args); }
  card.appendChild(pre);

  const row = node('row');
  const yes = Object.assign(document.createElement('button'), {className:'yes', textContent:'Approve'});
  const no  = Object.assign(document.createElement('button'), {className:'no',  textContent:'Reject'});
  const decide = ok => async () => {
    yes.disabled = no.disabled = true;
    try{
      await api('/v1/sessions/' + current + '/approve',
        {method:'POST', body: JSON.stringify({approved: ok})});
      card.replaceWith(node('note', ok ? 'approved' : 'rejected'));
    }catch(e){
      card.appendChild(node('note', e.message));
      yes.disabled = no.disabled = false;
    }
  };
  yes.onclick = decide(true); no.onclick = decide(false);
  row.append(yes, no);
  card.appendChild(row);
  ($('tx')).appendChild(card);
}

/* ------------------------------------------------------------------ inspector */
function drawInspector(){
  const el = $('insp');
  if(!current){ el.innerHTML = '<div class="insp-empty">Run metrics appear here once a session is selected.</div>'; return; }
  el.textContent = '';

  // Run
  const run = node('insp-sec');
  run.appendChild(Object.assign(document.createElement('h3'), {textContent:'Run'}));
  run.append(
    row('status', stats.reason || (live ? 'running' : 'idle')),
    row('turns', stats.turns),
    row('compactions', stats.compactions));
  el.appendChild(run);

  // Tokens — the numbers that decide capacity on owned GPUs.
  const tok = node('insp-sec');
  tok.appendChild(Object.assign(document.createElement('h3'), {textContent:'Tokens'}));
  tok.append(
    row('input', stats.tin.toLocaleString()),
    row('output', stats.tout.toLocaleString()),
    row('cached', stats.cached.toLocaleString()));
  const rate = stats.tin ? stats.cached / stats.tin : 0;
  tok.appendChild(row('cache hit', (rate*100).toFixed(0) + '%'));
  const meter = node('meter');
  meter.appendChild(Object.assign(document.createElement('i'),
    {style:'width:' + Math.round(rate*100) + '%'}));
  tok.appendChild(meter);
  if(stats.tin && !stats.cached){
    tok.appendChild(node('insp-empty',
      'This endpoint reports no prefix caching. Every turn pays full prefill.'));
  }
  el.appendChild(tok);

  // Tool tallies
  const names = Object.keys(stats.tools).sort((a,b) => stats.tools[b]-stats.tools[a]);
  if(names.length){
    const t = node('insp-sec');
    t.appendChild(Object.assign(document.createElement('h3'), {textContent:'Tool calls'}));
    for(const n of names){
      const line = node('tally');
      line.append(Object.assign(document.createElement('span'), {textContent:n}),
                  Object.assign(document.createElement('span'), {textContent:stats.tools[n]}));
      t.appendChild(line);
    }
    el.appendChild(t);
  }
}

function row(k, v){
  const d = document.createElement('dl');
  d.className = 'kv';
  d.append(Object.assign(document.createElement('dt'), {textContent:k}),
           Object.assign(document.createElement('dd'), {textContent:String(v)}));
  return d;
}

/* ------------------------------------------------------------------ actions */
$('go').onclick = async () => {
  const prompt = $('q').value.trim();
  if(!prompt) return;
  $('go').disabled = true;
  try{
    const r = await api('/v1/sessions',
      {method:'POST', body: JSON.stringify({prompt, mode: $('mode').value})});
    $('q').value = '';
    openSession(r.session_id);
    // Render the prompt immediately: a cold local model can take ~30s for its
    // first token, and an empty pane reads as failure.
    const b = node('said user');
    b.append(node('who','you'), document.createTextNode(prompt));
    $('tx').appendChild(b);
    newTurn();
    $('tx').appendChild(node('note','waiting for the model…'));
  }catch(e){
    alert(e.message);
  }finally{
    $('go').disabled = false;
  }
};

$('q').addEventListener('keydown', e => {
  if((e.metaKey || e.ctrlKey) && e.key === 'Enter') $('go').click();
});

$('stop').onclick = async () => {
  if(!current) return;
  try{ await api('/v1/sessions/' + current + '/interrupt', {method:'POST'}); }catch{}
};

// Show who is signed in when authentication is configured. A 401 simply means
// this deployment runs without it, which is a valid single-tenant setup.
async function whoami(){
  try{
    const me = await api('/v1/whoami');
    if(!me.authenticated) return;
    $('who').textContent = me.email || me.name || me.subject;
    $('who').title = 'tenant ' + me.tenant +
      (me.groups && me.groups.length ? ' · ' + me.groups.join(', ') : '');
    $('whobox').hidden = false;
  }catch{}
}

whoami(); health(); refresh();
setInterval(health, 10000);
setInterval(refresh, 5000);
</script>
</body>
</html>`, "\x00", "")
