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
  --rail:272px; --drawer:420px;
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
.top{height:48px;display:flex;align-items:center;gap:14px;padding:0 16px;
  background:var(--surface);border-bottom:1px solid var(--line);flex:none;
  box-shadow:0 1px 0 rgba(0,0,0,.04),0 2px 8px -6px rgba(0,0,0,.28);
  position:relative;z-index:3}
.brand{display:flex;align-items:center;gap:8px}
.mark{width:19px;height:19px;fill:var(--accent);flex:none;
  filter:drop-shadow(0 1px 2px rgba(0,0,0,.18))}
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
.shell{display:grid;grid-template-columns:var(--rail) minmax(0,1fr) 0;
  height:calc(100vh - 48px);transition:grid-template-columns .18s ease}
.shell.open{grid-template-columns:var(--rail) minmax(0,1fr) var(--drawer)}
@media (prefers-reduced-motion:reduce){.shell{transition:none}}
.rail{background:var(--surface);border-right:1px solid var(--line);
  display:flex;flex-direction:column;min-height:0}
.stage{display:flex;flex-direction:column;min-height:0;background:var(--bg)}
/* The drawer opens only when there is something to look at: a file the agent
   read or wrote, or output worth reading in full. Closed by default, so the
   conversation gets the width it deserves. */
.drawer{background:var(--surface);border-left:1px solid var(--line);
  overflow:hidden;min-height:0;display:flex;flex-direction:column}
.shell:not(.open) .drawer{border-left:0}
.drawer-head{height:38px;display:flex;align-items:center;gap:8px;padding:0 10px 0 15px;
  border-bottom:1px solid var(--line);flex:none;font-family:var(--mono);font-size:11px}
.drawer-head .name{color:var(--ink);overflow:hidden;text-overflow:ellipsis;
  white-space:nowrap;flex:1}
.drawer-head .kind{color:var(--muted);flex:none}
.drawer-body{flex:1;overflow:auto;min-height:0}
.drawer-body pre{margin:0;padding:14px 16px;font-family:var(--mono);font-size:11.5px;
  line-height:1.6;color:var(--ink-2);white-space:pre;tab-size:4}
.drawer-body .ln{color:var(--muted);user-select:none;display:inline-block;
  width:3.2em;text-align:right;padding-right:1.1em}
.x{background:none;border:0;color:var(--muted);cursor:pointer;font-size:16px;
  line-height:1;padding:2px 6px;border-radius:4px;flex:none}
.x:hover{background:var(--sunken);color:var(--ink)}
.openfile{display:block;margin:0 0 7px;background:var(--surface);
  border:1px solid var(--line);border-radius:5px;padding:3px 9px;
  font-family:var(--mono);font-size:10.5px;color:var(--accent);cursor:pointer}
.openfile:hover{border-color:var(--accent);background:var(--accent-soft)}

/* ---------------------------------------------------------------- composer */
.composer{padding:11px;border-bottom:1px solid var(--line);flex:none}
.new{width:100%;display:flex;align-items:center;justify-content:center;gap:7px;
  background:var(--sunken);border:1px solid var(--line);border-radius:7px;
  padding:8px 12px;font-size:12.5px;font-weight:550;cursor:pointer;color:var(--ink);
  transition:border-color .14s,background .14s}
.new:hover{border-color:var(--accent);background:var(--accent-soft)}
.new span{font-size:15px;line-height:1;color:var(--accent)}

/* The dock is the chat input: under the conversation, grows with the text,
   never scrolls away. */
.dock{flex:none;padding:10px 22px 16px;
  background:linear-gradient(to bottom,transparent,var(--bg) 24%)}
.dockwrap{max-width:760px;margin:0 auto;background:var(--surface);
  border:1px solid var(--line);border-radius:12px;padding:10px 12px 8px;
  box-shadow:0 2px 12px -6px rgba(0,0,0,.3)}
.dockwrap:focus-within{border-color:var(--accent);
  box-shadow:0 0 0 3px var(--accent-soft),0 2px 12px -6px rgba(0,0,0,.3)}
.dockrow{display:flex;align-items:center;gap:9px;margin-top:7px}
.dockhint{flex:1;font-family:var(--mono);font-size:10px;color:var(--muted)}
textarea{width:100%;min-height:22px;max-height:180px;resize:none;background:none;
  border:0;padding:0;font-size:13.5px;line-height:1.6;display:block}
textarea::placeholder{color:var(--muted)}
textarea:focus{outline:none}
.composer .row{display:flex;gap:8px;margin-top:8px;align-items:stretch}
select{background:var(--sunken);border:1px solid var(--line);border-radius:6px;
  padding:0 26px 0 9px;font-size:12px;font-family:var(--mono);cursor:pointer;
  appearance:none;background-image:linear-gradient(45deg,transparent 50%,currentColor 50%),
    linear-gradient(135deg,currentColor 50%,transparent 50%);
  background-position:calc(100% - 14px) 52%,calc(100% - 9px) 52%;
  background-size:5px 5px,5px 5px;background-repeat:no-repeat}
.go{width:28px;height:28px;flex:none;background:var(--accent);border:0;color:#fff;
  border-radius:50%;font-size:14px;line-height:1;cursor:pointer;display:grid;
  place-items:center;transition:transform .12s}
.go:hover:not(:disabled){transform:scale(1.06)}
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
.transcript{flex:1;overflow-y:auto;padding:22px 22px 8px;min-height:0}
.transcript > *{max-width:760px;margin-left:auto;margin-right:auto}
.empty{display:flex;flex-direction:column;align-items:center;justify-content:center;
  height:100%;gap:8px;color:var(--muted);text-align:center}
.empty .k{font-family:var(--mono);font-size:12px}
.empty .s{font-size:12.5px;max-width:36ch;line-height:1.65}
.mark-lg{width:40px;height:40px;fill:var(--accent);opacity:.28;margin-bottom:2px}
.empty .ex{display:flex;flex-direction:column;gap:6px;margin-top:16px;width:100%;
  max-width:330px}
.chip{background:var(--surface);border:1px solid var(--line);border-radius:7px;
  padding:8px 11px;font-size:12px;color:var(--ink-2);cursor:pointer;text-align:left;
  transition:border-color .14s,transform .14s}
.chip:hover{border-color:var(--accent);transform:translateY(-1px);color:var(--ink)}

/* turn grouping: a vertical spine ties a turn's calls together */
.turn{position:relative;padding-left:20px;margin-bottom:4px}
.turn::before{content:"";position:absolute;left:5px;top:14px;bottom:2px;
  width:1px;background:var(--line)}
.turn:last-child::before{display:none}

.said{background:var(--raised);border:1px solid var(--line);border-radius:9px;
  padding:12px 15px;margin:0 0 12px;white-space:pre-wrap;line-height:1.65;
  overflow-wrap:anywhere;box-shadow:0 1px 2px rgba(0,0,0,.05);
  animation:rise .22s cubic-bezier(.2,.7,.3,1) both}
@keyframes rise{from{opacity:0;transform:translateY(4px)}to{opacity:1;transform:none}}
@media (prefers-reduced-motion:reduce){.said{animation:none}}
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
.thinking{display:flex;align-items:center;gap:9px;font-family:var(--mono);
  font-size:11.5px;color:var(--muted);padding:9px 0 2px}
.thinking .bars{display:flex;gap:2.5px;align-items:flex-end;height:12px}
.thinking .bars i{width:2.5px;height:4px;background:var(--accent);border-radius:1px;
  animation:pulse 1.05s ease-in-out infinite}
.thinking .bars i:nth-child(2){animation-delay:.13s}
.thinking .bars i:nth-child(3){animation-delay:.26s}
.thinking .bars i:nth-child(4){animation-delay:.39s}
@keyframes pulse{0%,100%{height:4px;opacity:.45}50%{height:12px;opacity:1}}
@media (prefers-reduced-motion:reduce){.thinking .bars i{animation:none;height:8px}}
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
  <div class="brand">
    <svg class="mark" viewBox="0 0 24 24" aria-hidden="true">
      <!-- Doric column: capital, fluted shaft, base. The same mark the CLI
           draws in box characters. -->
      <rect x="3"  y="3"  width="18" height="3"   rx="1"/>
      <rect x="9"  y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
      <rect x="11.7" y="7.5" width="1.6" height="9" rx=".6"/>
      <rect x="14.4" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
      <rect x="3"  y="18" width="18" height="3"   rx="1"/>
    </svg>
    <b>Titan</b><span id="ver">console</span>
  </div>
  <div class="stat"><span class="led" id="led"></span><span id="health">connecting</span></div>
  <div class="spacer"></div>
  <div class="stat">model <b id="mdl">—</b></div>
  <div class="stat">active <b id="active">0</b></div>
  <div class="stat" id="whobox" hidden>
    <span class="who-chip" id="who"></span>
    <a class="ghost" id="switchuser" href="/switch-user"
       title="Sign in as a different user">Switch</a>
    <a class="ghost" id="signout" href="/logout">Sign out</a>
  </div>
</div>

<div class="shell">
  <!-- session rail -->
  <aside class="rail">
    <div class="composer">
      <button class="new" id="new" type="button"><span>+</span> New chat</button>
    </div>
    <div class="rail-head"><span>Chats</span><span id="count"></span></div>
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
        <svg class="mark-lg" viewBox="0 0 24 24" aria-hidden="true">
          <rect x="3" y="3" width="18" height="3" rx="1"/>
          <rect x="9" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
          <rect x="11.7" y="7.5" width="1.6" height="9" rx=".6"/>
          <rect x="14.4" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
          <rect x="3" y="18" width="18" height="3" rx="1"/>
        </svg>
        <div class="k">Ready</div>
        <div class="s">Ask a question, or describe a change. Select a session on the
          left to replay exactly what it did.</div>
        <div class="ex" id="examples"></div>
      </div>
    </div>
    <div class="dock">
      <div class="dockwrap">
        <textarea id="q" rows="1" placeholder="Ask anything, or describe a change…"></textarea>
        <div class="dockrow">
          <select id="mode" title="Permission mode">
            <option value="default">default</option>
            <option value="plan">plan</option>
            <option value="accept-edits">accept-edits</option>
            <option value="auto">auto</option>
          </select>
          <span class="dockhint">Enter sends · Shift+Enter for a new line</span>
          <button class="go" id="go" type="button" title="Send">↑</button>
        </div>
      </div>
    </div>
  </main>

  <aside class="drawer" id="drawer" aria-hidden="true">
    <div class="drawer-head">
      <span class="name" id="dname"></span>
      <span class="kind" id="dkind"></span>
      <button class="x" id="dclose" type="button" title="Close" aria-label="Close">×</button>
    </div>
    <div class="drawer-body" id="dbody"></div>
  </aside>
</div>

<script>
"use strict";
const $ = id => document.getElementById(id);

let current = null;      // session id being viewed
let streamEl = null;     // bubble currently receiving streamed text
let streamBody = null;   // its text node
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
  streamEl = null; streamBody = null;
  calls.clear();
  Object.assign(stats, {turns:0, tin:0, tout:0, cached:0, tools:{}, reason:null, compactions:0});

  $('tx').textContent = '';
  $('sid').textContent = id;
  $('stop').hidden = false;
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

    case 'agent.delta': {
      // Stream into a bubble created on the first fragment. agent.message
      // arrives afterwards with the complete text and closes the bubble
      // rather than appending a second copy.
      hideThinking();
      if(!streamEl){
        streamEl = node('said');
        streamEl.append(node('who','titan'));
        streamBody = document.createTextNode('');
        streamEl.appendChild(streamBody);
        (turnEl || tx).appendChild(streamEl);
      }
      streamBody.appendData(p.text || '');
      break;
    }

    case 'agent.message': {
      hideThinking();
      if(streamEl){
        // The deltas already rendered this. Reconcile against the
        // authoritative text in case a fragment was dropped on reconnect,
        // then close the bubble.
        if((p.text || '') !== streamBody.data) streamBody.data = p.text || '';
        streamEl = null; streamBody = null;
        break;
      }
      if(!(p.text || '').trim()) break;
      const b = node('said');
      b.append(node('who','titan'), document.createTextNode(p.text));
      (turnEl || tx).appendChild(b);
      break;
    }

    case 'action.requested': {
      hideThinking();
      // A tool call ends the current streamed reply.
      streamEl = null; streamBody = null;
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
      // Anything worth reading in full opens in the drawer rather than
      // stretching the conversation column.
      const owner = calls.get(p.call_id);
      const isFile = ['read','write','edit'].includes(p.tool);
      const long = (p.content || '').split('\n').length > 12;
      if((isFile || long) && !p.is_error){
        const b = document.createElement('button');
        b.className = 'openfile'; b.type = 'button';
        b.textContent = isFile ? 'Open file' : 'View output';
        const label = owner ? (owner.querySelector('.arg') || {}).textContent || p.tool : p.tool;
        b.onclick = () => openDrawer(label, p.tool, p.content || '', isFile);
        body.appendChild(b);
      }
      if(ev.trust === 'untrusted' && !p.is_error){
        // Provenance is a first-class concept in Titan: tool output is data,
        // never instruction. Saying so in the UI keeps that visible.
        body.appendChild(node('tag','untrusted data'));
      }
      body.appendChild(document.createTextNode(clip(p.content || '', 4000)));
      wrap.appendChild(body);

      if(live) showThinking('working');
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
      hideThinking();
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

/* ------------------------------------------------------------------ actions */
$('go').onclick = send;

// The first message opens a session; every later one continues it. That
// distinction is what makes "now add a test for it" resolve against what came
// before, instead of starting a fresh conversation each time.
async function send(){
  const prompt = $('q').value.trim();
  if(!prompt) return;
  $('go').disabled = true;
  try{
    if(current && !live){
      await api('/v1/sessions/' + current + '/messages',
        {method:'POST', body: JSON.stringify({prompt})});
      $('q').value = ''; autogrow();
      live = true;
      showThinking('waiting for the model');
      if(!es) connect(current);
    }else{
      const r = await api('/v1/sessions',
        {method:'POST', body: JSON.stringify({prompt, mode: $('mode').value})});
      $('q').value = ''; autogrow();
      openSession(r.session_id);
      showThinking('waiting for the model');
    }
  }catch(e){
    $('tx').appendChild(node('note', e.message));
  }finally{
    $('go').disabled = false;
    $('q').focus();
  }
}

$('new').onclick = () => {
  if(es){ es.close(); es = null; }
  current = null; live = false; lastSeq = 0; turnEl = null;
  calls.clear();
  Object.assign(stats, {turns:0, tin:0, tout:0, cached:0, tools:{}, reason:null, compactions:0});
  $('sid').textContent = 'new chat';
  $('stop').hidden = true;
  closeDrawer();
  drawEmpty();
  refresh();
  $('q').focus();
};

// The textarea grows with its content, like every chat input people know.
function autogrow(){
  const t = $('q');
  t.style.height = 'auto';
  t.style.height = Math.min(t.scrollHeight, 180) + 'px';
}
$('q').addEventListener('input', autogrow);

/* ---------------------------------------------------------------- drawer */
function openDrawer(name, kind, body, numbered){
  $('dname').textContent = name;
  $('dkind').textContent = kind || '';
  const pre = document.createElement('pre');
  if(numbered){
    // read() returns numbered lines; keep the gutter separate so the code
    // itself stays selectable and copyable.
    for(const line of body.split('\n')){
      const m = line.match(/^\s*(\d+)\t(.*)$/);
      if(m){
        const g = document.createElement('span');
        g.className = 'ln'; g.textContent = m[1];
        pre.append(g, document.createTextNode(m[2] + '\n'));
      }else{
        pre.append(document.createTextNode(line + '\n'));
      }
    }
  }else{
    pre.textContent = body;
  }
  const host = $('dbody');
  host.textContent = '';
  host.appendChild(pre);
  document.querySelector('.shell').classList.add('open');
  $('drawer').setAttribute('aria-hidden','false');
}

function closeDrawer(){
  document.querySelector('.shell').classList.remove('open');
  $('drawer').setAttribute('aria-hidden','true');
}
$('dclose').onclick = closeDrawer;
document.addEventListener('keydown', e => { if(e.key === 'Escape') closeDrawer(); });

function drawEmpty(){
  const tx = $('tx');
  tx.textContent = '';
  const wrap = node('empty');
  wrap.innerHTML =
    '<svg class="mark-lg" viewBox="0 0 24 24" aria-hidden="true">' +
    '<rect x="3" y="3" width="18" height="3" rx="1"/>' +
    '<rect x="9" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>' +
    '<rect x="11.7" y="7.5" width="1.6" height="9" rx=".6"/>' +
    '<rect x="14.4" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>' +
    '<rect x="3" y="18" width="18" height="3" rx="1"/></svg>' +
    '<div class="k">Ready</div>' +
    '<div class="s">Ask a question, or describe a change. Follow-ups continue ' +
    'the same conversation.</div><div class="ex" id="examples"></div>';
  tx.appendChild(wrap);
  drawExamples();
}

$('q').addEventListener('keydown', e => {
  if(e.key === 'Enter' && !e.shiftKey){ e.preventDefault(); send(); }
});

$('stop').onclick = async () => {
  if(!current) return;
  try{ await api('/v1/sessions/' + current + '/interrupt', {method:'POST'}); }catch{}
};

// Show who is signed in when authentication is configured. A 401 simply means
// this deployment runs without it, which is a valid single-tenant setup.
async function whoami(){
  let me = null;
  try{ me = await api('/v1/whoami'); }catch{}

  if(!me || !me.authenticated){
    // No signed-in user. REMOVE the chip rather than hiding it: a hidden
    // control is still in the document, and a Sign out link that leads
    // nowhere is worse than no link at all. This is what produced a 404
    // when auth was never configured.
    const box = $('whobox');
    if(box && box.parentNode) box.parentNode.removeChild(box);
    return;
  }

  $('who').textContent = me.email || me.name || me.subject;
  $('who').title = 'tenant ' + me.tenant +
    (me.groups && me.groups.length ? ' · ' + me.groups.join(', ') : '');
  $('whobox').hidden = false;
}

// A first-run console that only says "ask something" teaches nothing. These
// are the three shapes Titan handles, so the examples double as documentation.
const EXAMPLES = [
  ['Explain a concept',  'What is z/OS and where is it used?'],
  ['Understand code',    'What does the Valid function do in this codebase?'],
  ['Make a change',      'The tests in pkg/auth are failing. Find the bug and fix it.'],
];

function drawExamples(){
  const box = $('examples');
  if(!box) return;
  box.textContent = '';
  for(const [label, text] of EXAMPLES){
    const b = document.createElement('button');
    b.className = 'chip';
    b.type = 'button';
    const strong = document.createElement('div');
    strong.style.cssText = 'font-family:var(--mono);font-size:9.5px;letter-spacing:.08em;' +
      'text-transform:uppercase;color:var(--muted);margin-bottom:3px';
    strong.textContent = label;
    b.append(strong, document.createTextNode(text));
    b.onclick = () => { $('q').value = text; $('q').focus(); };
    box.appendChild(b);
  }
}

// A live indicator while the model is working. A cold 30B model can take ~30s
// for its first token, and a static line is indistinguishable from a hang.
function showThinking(what){
  hideThinking();
  const el = node('thinking');
  el.id = 'thinking';
  const bars = node('bars');
  for(let i=0;i<4;i++) bars.appendChild(document.createElement('i'));
  const label = document.createElement('span');
  label.textContent = what || 'thinking';
  const clock = document.createElement('span');
  clock.style.cssText = 'color:var(--muted);font-variant-numeric:tabular-nums';
  el.append(bars, label, clock);
  $('tx').appendChild(el);

  const t0 = Date.now();
  el.dataset.timer = setInterval(() => {
    clock.textContent = ((Date.now()-t0)/1000).toFixed(1) + 's';
  }, 100);
  $('tx').scrollTop = $('tx').scrollHeight;
}

function hideThinking(){
  const el = $('thinking');
  if(!el) return;
  clearInterval(Number(el.dataset.timer));
  el.remove();
}

drawExamples(); whoami(); health(); refresh();
setInterval(health, 10000);
setInterval(refresh, 5000);
</script>
</body>
</html>`, "\x00", "")

// authDisabledHTML is shown when someone reaches /login or /logout on a server
// running without authentication. It says what is true and what to change,
// rather than leaving a 404 that looks like a fault.
const authDisabledHTML = `<!doctype html><meta charset="utf-8">
<title>Sign-in not configured</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0B0E13;
  color:#E8EDF4;font:14px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif}
@media (prefers-color-scheme:light){body{background:#F5F7FA;color:#0F141B}}
.card{max-width:520px;padding:30px 34px;border-radius:12px;background:#141922;
  border:1px solid #252D3A}
@media (prefers-color-scheme:light){.card{background:#fff;border-color:#DCE3EC}}
h1{margin:0 0 10px;font-size:17px;display:flex;align-items:center;gap:9px}
svg{width:19px;height:19px;fill:#4C8FD6}
p{margin:0 0 12px;color:#8A96A8}
pre{background:#0F141C;border:1px solid #252D3A;border-radius:7px;padding:12px 14px;
  font:11.5px/1.6 ui-monospace,SFMono-Regular,Menlo,monospace;color:#BAC6D4;overflow-x:auto}
@media (prefers-color-scheme:light){pre{background:#EDF1F6;border-color:#DCE3EC;color:#3A4757}}
a{color:#4C8FD6}
</style>
<div class="card">
  <h1><svg viewBox="0 0 24 24" aria-hidden="true">
    <rect x="3" y="3" width="18" height="3" rx="1"/>
    <rect x="9" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
    <rect x="11.7" y="7.5" width="1.6" height="9" rx=".6"/>
    <rect x="14.4" y="7.5" width="1.6" height="9" rx=".6" opacity=".85"/>
    <rect x="3" y="18" width="18" height="3" rx="1"/></svg>
    Sign-in is not configured</h1>
  <p>This Titan server runs with <code>auth.mode: none</code> — a single-tenant
     setup with no user accounts, so there is nobody to sign in or out as.</p>
  <p>To enable sign-in, add an identity provider to your config:</p>
  <pre>{
  "auth": {
    "mode": "oidc",
    "issuer": "https://idp.internal/realms/engineering",
    "audience": "titan",
    "client_id": "titan-console",
    "client_secret_env": "TITAN_OIDC_SECRET",
    "redirect_url": "http://localhost:8420/auth/callback",
    "tenant_claim": "org_id"
  }
}</pre>
  <p>See <code>docs/ops/enabling-auth.md</code> for per-provider settings.</p>
  <p><a href="/">← Back to Titan</a></p>
</div>`
