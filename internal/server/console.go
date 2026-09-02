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
<title>Titan</title>
<style>
:root{
  --bg:#0E1116; --surface:#161B22; --surface2:#1C222B; --line:#28303B;
  --ink:#E6EDF3; --ink2:#C3CBD6; --muted:#8B949E;
  --accent:#4A9EDA; --ok:#3FB950; --warn:#D29922; --err:#F85149;
  --mono:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
}
@media (prefers-color-scheme:light){
  :root{--bg:#F6F7F9;--surface:#FFF;--surface2:#EDF0F4;--line:#D8DEE6;
        --ink:#11161D;--ink2:#3D4753;--muted:#6B7684;--accent:#1F6FB2;
        --ok:#1A7F37;--warn:#9A6700;--err:#C4342B;}
}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);
  font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif}
header{display:flex;align-items:center;gap:14px;padding:14px 20px;
  border-bottom:1px solid var(--line);background:var(--surface)}
header h1{margin:0;font-size:15px;font-weight:600;letter-spacing:-.01em}
header .meta{font-family:var(--mono);font-size:11.5px;color:var(--muted)}
header .dot{width:7px;height:7px;border-radius:50%;background:var(--muted)}
header .dot.up{background:var(--ok)}
main{display:grid;grid-template-columns:290px 1fr;height:calc(100vh - 53px)}
aside{border-right:1px solid var(--line);background:var(--surface);
  overflow-y:auto;display:flex;flex-direction:column}
.new{padding:14px;border-bottom:1px solid var(--line)}
textarea{width:100%;min-height:74px;resize:vertical;background:var(--bg);
  color:var(--ink);border:1px solid var(--line);border-radius:5px;padding:9px;
  font:inherit}
textarea:focus{outline:2px solid var(--accent);outline-offset:-1px}
.row{display:flex;gap:8px;margin-top:8px}
select,button{font:inherit;border-radius:5px;border:1px solid var(--line);
  background:var(--surface2);color:var(--ink);padding:7px 11px;cursor:pointer}
button.primary{background:var(--accent);border-color:var(--accent);color:#fff;
  font-weight:500;flex:1}
button:disabled{opacity:.5;cursor:not-allowed}
.sessions{flex:1;overflow-y:auto}
.sess{padding:11px 14px;border-bottom:1px solid var(--line);cursor:pointer}
.sess:hover{background:var(--surface2)}
.sess.active{background:var(--surface2);box-shadow:inset 3px 0 0 var(--accent)}
.sess .p{font-size:13px;margin-bottom:4px;overflow:hidden;
  text-overflow:ellipsis;white-space:nowrap}
.sess .s{font-family:var(--mono);font-size:10.5px;color:var(--muted);
  display:flex;gap:8px;align-items:center}
.badge{padding:1px 6px;border-radius:3px;font-size:10px;letter-spacing:.04em}
.badge.running{background:rgba(74,158,218,.16);color:var(--accent)}
.badge.done{background:rgba(63,185,80,.14);color:var(--ok)}
.badge.waiting_approval{background:rgba(210,153,34,.16);color:var(--warn)}
section{overflow-y:auto;padding:20px 26px}
.empty{color:var(--muted);text-align:center;margin-top:80px}
.ev{margin-bottom:9px;font-size:13.5px}
.ev .tool{font-family:var(--mono);font-size:12.5px}
.ev .tool b{color:var(--accent);font-weight:600}
.ev .args{color:var(--muted)}
.obs{font-family:var(--mono);font-size:11.5px;color:var(--ink2);
  background:var(--surface);border-left:2px solid var(--line);
  padding:7px 11px;margin:4px 0 0 14px;white-space:pre-wrap;
  overflow-x:auto;border-radius:0 4px 4px 0;max-height:280px;overflow-y:auto}
.obs.err{border-left-color:var(--err);color:var(--err)}
.obs.untrusted{border-left-color:var(--warn)}
.msg{background:var(--surface);border:1px solid var(--line);border-radius:6px;
  padding:12px 15px;margin:10px 0;white-space:pre-wrap}
.msg.user{background:var(--surface2)}
.approve{border:1px solid var(--warn);border-radius:6px;padding:14px;
  margin:12px 0;background:rgba(210,153,34,.07)}
.approve h4{margin:0 0 6px;font-size:13px;color:var(--warn);font-family:var(--mono)}
.approve pre{font-family:var(--mono);font-size:11.5px;background:var(--bg);
  padding:9px;border-radius:4px;overflow-x:auto;margin:8px 0}
.end{font-family:var(--mono);font-size:11.5px;color:var(--muted);
  border-top:1px solid var(--line);padding-top:10px;margin-top:14px}
</style>
</head>
<body>
<header>
  <h1>Titan</h1>
  <span class="dot" id="health"></span>
  <span class="meta" id="hmeta">connecting…</span>
</header>
<main>
  <aside>
    <div class="new">
      <textarea id="prompt" placeholder="Describe a task…"></textarea>
      <div class="row">
        <select id="mode">
          <option value="default">default</option>
          <option value="accept-edits">accept-edits</option>
          <option value="plan">plan</option>
          <option value="auto">auto</option>
        </select>
        <button class="primary" id="start">Start</button>
      </div>
    </div>
    <div class="sessions" id="sessions"></div>
  </aside>
  <section id="stream"><div class="empty">Select or start a session.</div></section>
</main>

<script>
const $ = id => document.getElementById(id);
let current = null, es = null, lastSeq = 0;

async function api(path, opts) {
  const r = await fetch(path, {headers:{'Content-Type':'application/json'}, ...opts});
  if (!r.ok) throw new Error((await r.json().catch(()=>({}))).error || r.statusText);
  return r.status === 204 ? null : r.json();
}

async function health() {
  try {
    const h = await api('/v1/health');
    $('health').className = 'dot up';
    $('hmeta').textContent = h.model + ' · ' + h.sessions + ' session(s)';
  } catch {
    $('health').className = 'dot';
    $('hmeta').textContent = 'unreachable';
  }
}

async function refresh() {
  try {
    const list = await api('/v1/sessions');
    const el = $('sessions');
    // Only the session LIST is re-rendered here. The stream panel is never
    // touched: an earlier version cleared it on every poll, which wiped output
    // out from under an active session every few seconds.
    el.innerHTML = '';
    list.sort((a,b) => new Date(b.created) - new Date(a.created));
    for (const s of list) {
      const d = document.createElement('div');
      d.className = 'sess' + (s.id === current ? ' active' : '');
      d.innerHTML = '<div class="p"></div><div class="s">' +
        '<span class="badge ' + s.state + '">' + s.state + '</span>' +
        '<span>' + s.user + '</span></div>';
      d.querySelector('.p').textContent = s.prompt;
      d.onclick = () => open(s.id);
      el.appendChild(d);
    }
  } catch {}
}

function open(id) {
  if (es) { es.close(); es = null; }
  current = id;
  $('stream').innerHTML = '';
  lastSeq = 0;
  refresh();
  connect(id);
}

// connect opens the event stream and keeps it open.
//
// EventSource fires onerror on any interruption — including the normal close
// when a session ends. Without a reconnect the panel silently stops updating,
// which looks exactly like "the agent did nothing" even while it is working.
function connect(id) {
  es = new EventSource('/v1/sessions/' + id + '/events');

  es.onmessage = e => {
    const ev = JSON.parse(e.data);
    if (ev.seq <= lastSeq) return;   // ignore replays after a reconnect
    lastSeq = ev.seq;
    render(ev);
    if (ev.type === 'session.ended') { es.close(); es = null; }
  };

  es.onerror = () => {
    if (!es) return;
    es.close(); es = null;
    // Retry unless the user moved to another session in the meantime.
    setTimeout(() => { if (current === id && !es) connect(id); }, 1500);
  };
}

function el(cls, text) {
  const d = document.createElement('div');
  d.className = cls;
  if (text !== undefined) d.textContent = text;
  return d;
}

function render(ev) {
  const out = $('stream');
  const p = ev.payload || {};

  if (ev.type === 'user.message') {
    out.appendChild(el('msg user', p.text));
  } else if (ev.type === 'agent.message') {
    out.appendChild(el('msg', p.text));
  } else if (ev.type === 'action.requested') {
    const d = el('ev');
    const t = el('tool');
    t.innerHTML = '● <b></b> <span class="args"></span>';
    t.querySelector('b').textContent = p.tool;
    t.querySelector('.args').textContent = summarize(p.tool, p.args);
    d.appendChild(t);
    out.appendChild(d);
    if (p.requires_approval) approvalCard(p);
  } else if (ev.type === 'observation') {
    let cls = 'obs';
    if (p.is_error) cls += ' err';
    else if (ev.trust === 'untrusted') cls += ' untrusted';
    out.appendChild(el(cls, (p.content || '').slice(0, 4000)));
  } else if (ev.type === 'compaction.completed') {
    out.appendChild(el('end',
      'context compacted: ' + p.before_tokens + ' → ' + p.after_tokens + ' tokens'));
  } else if (ev.type === 'session.ended') {
    let s = 'ended: ' + p.reason + ' · ' + p.turns + ' turns · ' +
            p.tokens_in + ' in / ' + p.tokens_out + ' out';
    if (p.tokens_cached) s += ' · ' + Math.round(100*p.tokens_cached/p.tokens_in) + '% cached';
    if (p.compactions) s += ' · ' + p.compactions + ' compaction(s)';
    out.appendChild(el('end', s));
    refresh();
  }
  out.scrollTop = out.scrollHeight;
}

function summarize(tool, args) {
  if (!args) return '';
  try {
    const a = typeof args === 'string' ? JSON.parse(args) : args;
    if (tool === 'bash') return a.description || a.command || '';
    return a.path || a.pattern || a.query || a.description || '';
  } catch { return ''; }
}

function approvalCard(p) {
  const card = el('approve');
  const h = el('');
  h.innerHTML = '<h4></h4>';
  h.querySelector('h4').textContent = 'Approval required: ' + p.tool;
  card.appendChild(h);
  if (p.reason) card.appendChild(el('', p.reason));

  const pre = document.createElement('pre');
  try {
    pre.textContent = JSON.stringify(
      typeof p.args === 'string' ? JSON.parse(p.args) : p.args, null, 2);
  } catch { pre.textContent = String(p.args); }
  card.appendChild(pre);

  const row = el('row');
  const yes = document.createElement('button');
  yes.className = 'primary';
  yes.textContent = 'Approve';
  const no = document.createElement('button');
  no.textContent = 'Reject';
  const decide = approved => async () => {
    yes.disabled = no.disabled = true;
    try {
      await api('/v1/sessions/' + current + '/approve',
        {method:'POST', body: JSON.stringify({approved})});
      card.remove();
    } catch (e) { card.appendChild(el('', e.message)); }
  };
  yes.onclick = decide(true);
  no.onclick = decide(false);
  row.appendChild(yes); row.appendChild(no);
  card.appendChild(row);
  $('stream').appendChild(card);
}

$('start').onclick = async () => {
  const prompt = $('prompt').value.trim();
  if (!prompt) return;
  $('start').disabled = true;
  try {
    const r = await api('/v1/sessions',
      {method:'POST', body: JSON.stringify({prompt, mode: $('mode').value})});
    const sent = prompt;
    $('prompt').value = '';
    open(r.session_id);
    // The first model call can take ~30s on a cold local model. Say so, rather
    // than showing a blank panel that reads as failure.
    const note = el('msg user', sent);
    $('stream').appendChild(note);
    $('stream').appendChild(el('end', 'waiting for the model…'));
  } catch (e) {
    alert(e.message);
  } finally {
    $('start').disabled = false;
  }
};

$('prompt').addEventListener('keydown', e => {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') $('start').click();
});

health(); refresh();
setInterval(health, 15000);
setInterval(refresh, 4000);
</script>
</body>
</html>`, "\x00", "")
