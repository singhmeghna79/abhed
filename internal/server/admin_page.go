package server

import (
	"net/http"
)

// serveAdmin serves the access dashboard.
//
// A separate page rather than a panel inside the console: the console is for
// running the agent, and this is for deciding who may. Mixing them puts a
// revoke button next to a chat box.
//
// Everything it shows comes from /v1/admin/access, which is admin-guarded, so
// a non-admin who loads this page is told so rather than shown data.
func (s *Server) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; "+
			"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Write([]byte(withHome(adminHTML, s.opts.HomeURL)))
}

const adminHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Access — Titan</title>
<style>
:root{
  --bg:#F4F6FA; --surface:#FFFFFF; --sunken:#E6EBF3;
  --line:#D2DAE6; --line-strong:#B0BCCC;
  --ink:#0D1218; --ink-2:#38424F; --muted:#66727F;
  --accent:#0F63C4; --accent-soft:#E2EDFB;
  --ok:#1A7F4B; --ok-bg:#E3F3EA;
  --stop:#B3261E; --stop-bg:#FBE9E7;
  --mono:ui-monospace,SFMono-Regular,Menlo,monospace;
  --sans:-apple-system,BlinkMacSystemFont,"Segoe UI",system-ui,sans-serif;
}
@media (prefers-color-scheme:dark){:root:not([data-theme="light"]){
  --bg:#070B12; --surface:#0E1521; --sunken:#0A101A;
  --line:#1B2636; --line-strong:#2B3B52;
  --ink:#E4ECF7; --ink-2:#AFBED2; --muted:#77879D;
  --accent:#3BA9FF; --accent-soft:#0B2540;
  --ok:#3FAF6C; --ok-bg:#0F2419;
  --stop:#F2777A; --stop-bg:#2C1416;
}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font-family:var(--sans);
  font-size:14.5px;line-height:1.55}
a{color:var(--accent)}
.wrap{max-width:1180px;margin:0 auto;padding:0 22px}
header{border-bottom:1px solid var(--line);background:var(--surface)}
.bar{display:flex;align-items:center;gap:14px;height:54px}
.bar b{font-family:var(--mono);font-size:15px;letter-spacing:-.02em}
.bar .sp{margin-left:auto}
.bar a{font-family:var(--mono);font-size:12.5px;color:var(--ink-2);text-decoration:none}
.bar a:hover{color:var(--accent)}
h1{font-size:21px;margin:26px 0 4px;letter-spacing:-.02em}
.sub{color:var(--muted);margin:0 0 20px;font-size:13.5px;max-width:70ch}

.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));
  gap:1px;background:var(--line);border:1px solid var(--line);border-radius:10px;
  overflow:hidden;margin-bottom:22px}
.cards>div{background:var(--surface);padding:15px 17px}
.cards b{display:block;font-family:var(--mono);font-size:26px;letter-spacing:-.03em;
  font-variant-numeric:tabular-nums}
.cards span{display:block;margin-top:3px;font-size:11.5px;color:var(--muted);
  text-transform:uppercase;letter-spacing:.07em}

.tw{overflow-x:auto;border:1px solid var(--line);border-radius:10px;background:var(--surface)}
table{border-collapse:collapse;width:100%;font-size:13.5px}
th,td{text-align:left;padding:10px 13px;border-bottom:1px solid var(--line);vertical-align:top}
th{font-family:var(--mono);font-size:10.5px;letter-spacing:.08em;text-transform:uppercase;
  color:var(--muted);font-weight:400;background:var(--sunken);white-space:nowrap}
tbody tr:last-child td{border-bottom:0}
tbody tr:hover{background:var(--sunken)}
td.who b{display:block}
td.who span{color:var(--muted);font-size:12.5px}
td .mono{font-family:var(--mono);font-size:11.5px;color:var(--muted)}

.pill{display:inline-block;font-family:var(--mono);font-size:10.5px;letter-spacing:.06em;
  text-transform:uppercase;padding:2px 8px;border-radius:999px;white-space:nowrap}
.pill.active{background:var(--ok-bg);color:var(--ok)}
.pill.pending{background:var(--accent-soft);color:var(--accent)}
.pill.revoked{background:var(--stop-bg);color:var(--stop)}
.pill.expired{background:var(--sunken);color:var(--muted)}

button{font:inherit;cursor:pointer;border-radius:6px;border:1px solid var(--line-strong);
  background:var(--surface);color:var(--ink);padding:5px 11px;font-size:12.5px}
button:hover{border-color:var(--accent);color:var(--accent)}
button.danger{border-color:var(--stop);color:var(--stop)}
button.danger:hover{background:var(--stop-bg)}
button:disabled{opacity:.5;cursor:default}

dialog{border:1px solid var(--line);border-radius:12px;background:var(--surface);
  color:var(--ink);padding:0;max-width:520px;width:calc(100% - 40px)}
dialog::backdrop{background:rgba(0,0,0,.5)}
.dlg{padding:22px}
.dlg h2{margin:0 0 6px;font-size:17px}
.dlg p{margin:0 0 16px;color:var(--ink-2);font-size:13.5px}
.dlg label{display:block;font-family:var(--mono);font-size:10.5px;letter-spacing:.08em;
  text-transform:uppercase;color:var(--muted);margin:0 0 5px}
.dlg select,.dlg textarea{width:100%;font:inherit;font-size:13.5px;padding:8px 10px;
  border:1px solid var(--line);border-radius:6px;background:var(--sunken);color:var(--ink);
  margin-bottom:14px}
.dlg textarea{min-height:70px;resize:vertical}
.dlg .row{display:flex;gap:9px;justify-content:flex-end}
.msg{padding:11px 14px;border-radius:8px;margin-bottom:16px;font-size:13.5px;display:none}
.msg.show{display:block}
.msg.err{background:var(--stop-bg);color:var(--stop)}
.msg.ok{background:var(--ok-bg);color:var(--ok)}
.empty{padding:34px;text-align:center;color:var(--muted);font-size:13.5px}
</style>
</head>
<header><div class="wrap bar">
  <b>Titan</b><span style="color:var(--muted);font-size:12.5px">access</span>
  <span class="sp"></span>
  <a href="/console">Console</a>
  <a href="/docs/">Docs</a><!--HOME-->
</div></header>

<main class="wrap">
  <h1>Who has access</h1>
  <p class="sub">Everyone who has asked for the console, what happened, and what
    is true now. Revoking ends their session immediately and emails them the
    clause it was done under.</p>

  <div class="msg" id="msg"></div>
  <div class="cards" id="cards"></div>

  <div class="tw">
    <table>
      <thead><tr>
        <th>Person</th><th>Status</th><th>Account</th>
        <th>Requested</th><th>Expires</th><th></th>
      </tr></thead>
      <tbody id="rows"></tbody>
    </table>
    <div class="empty" id="empty" hidden>Nobody has requested access yet.</div>
  </div>
</main>

<dialog id="dlg"><form method="dialog" class="dlg">
  <h2>End access</h2>
  <p id="dlgwho"></p>
  <label for="clause">Policy clause</label>
  <select id="clause">
    <option value="P1">P1 — attacking the service</option>
    <option value="P2">P2 — using it to attack others</option>
    <option value="P3">P3 — illegal content or purpose</option>
    <option value="P4">P4 — automated or resource abuse</option>
    <option value="P5">P5 — misrepresentation</option>
    <option value="P6">P6 — capacity, not their fault</option>
    <option value="P7">P7 — they asked us to</option>
  </select>
  <label for="note">Note (kept for the record, not sent to them)</label>
  <textarea id="note" placeholder="What actually happened."></textarea>
  <div class="row">
    <button value="cancel">Cancel</button>
    <button class="danger" id="go" value="revoke" type="button">Revoke and notify</button>
  </div>
</form></dialog>

<script>
"use strict";
var $ = function (id) { return document.getElementById(id); };
var GRANTS = [];

function say(text, kind) {
  var m = $('msg');
  m.textContent = text;
  m.className = 'msg show ' + kind;
}

function when(iso) {
  if (!iso) return '—';
  var d = new Date(iso);
  if (isNaN(d)) return '—';
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' });
}

// The state a person is actually in, which is not always the stored status: a
// granted row whose expiry has passed is expired, and calling it active would
// be the dashboard lying about the one thing it exists to report.
function stateOf(g) {
  if (g.status === 'revoked') return 'revoked';
  if (g.status === 'requested') return 'pending';
  if (g.expires_at && new Date(g.expires_at) < new Date()) return 'expired';
  if (g.status === 'granted') return 'active';
  return 'expired';
}

// Built with createElement rather than innerHTML throughout. Every value here
// is a string a stranger typed into a public form, and this page is looked at
// by an administrator with a revoke button.
function render() {
  var body = $('rows');
  body.textContent = '';
  $('empty').hidden = GRANTS.length > 0;

  GRANTS.forEach(function (g) {
    var st = stateOf(g);
    var tr = document.createElement('tr');

    var who = document.createElement('td');
    who.className = 'who';
    var b = document.createElement('b');
    b.textContent = g.name || '(no name)';
    var sp = document.createElement('span');
    sp.textContent = g.email + (g.company ? ' · ' + g.company : '');
    who.appendChild(b); who.appendChild(sp);

    var status = document.createElement('td');
    var pill = document.createElement('span');
    pill.className = 'pill ' + st;
    pill.textContent = st;
    status.appendChild(pill);
    if (g.revoked_code) {
      var rc = document.createElement('div');
      rc.className = 'mono';
      rc.textContent = g.revoked_code + ' · ' + when(g.revoked_at);
      status.appendChild(rc);
    }

    var acct = document.createElement('td');
    acct.className = 'mono';
    acct.textContent = g.username || '—';

    var req = document.createElement('td');
    req.textContent = when(g.requested_at);

    var exp = document.createElement('td');
    exp.textContent = when(g.expires_at);

    var act = document.createElement('td');
    if (st === 'active' || st === 'pending') {
      var btn = document.createElement('button');
      btn.className = 'danger';
      btn.textContent = 'Revoke';
      btn.addEventListener('click', function () { ask(g); });
      act.appendChild(btn);
    }

    [who, status, acct, req, exp, act].forEach(function (td) { tr.appendChild(td); });
    body.appendChild(tr);
  });
}

function counts(sum) {
  var c = $('cards');
  c.textContent = '';
  [['active', 'with access now'], ['pending', 'awaiting a decision'],
   ['revoked', 'revoked'], ['expired', 'lapsed'], ['total', 'ever requested']
  ].forEach(function (pair) {
    var d = document.createElement('div');
    var b = document.createElement('b');
    b.textContent = sum[pair[0]] || 0;
    var s = document.createElement('span');
    s.textContent = pair[1];
    d.appendChild(b); d.appendChild(s);
    c.appendChild(d);
  });
}

var target = null;
function ask(g) {
  target = g;
  $('dlgwho').textContent =
    (g.name || g.email) + ' loses access immediately and is emailed the clause.';
  $('note').value = '';
  $('dlg').showModal();
}

$('go').addEventListener('click', function () {
  if (!target) return;
  var btn = this;
  btn.disabled = true;
  btn.textContent = 'Revoking…';
  fetch('/v1/admin/access/' + encodeURIComponent(target.id) + '/revoke', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ clause: $('clause').value, note: $('note').value })
  }).then(function (r) {
    if (!r.ok) return r.json().then(function (e) { throw new Error(e.error || r.status); });
    return r.json();
  }).then(function () {
    $('dlg').close();
    say('Access ended. The notice is on its way.', 'ok');
    load();
  }).catch(function (e) {
    say('Could not revoke: ' + e.message, 'err');
  }).finally(function () {
    btn.disabled = false;
    btn.textContent = 'Revoke and notify';
  });
});

function load() {
  fetch('/v1/admin/access').then(function (r) {
    if (r.status === 403) throw new Error('This page needs an administrator account.');
    if (r.status === 501) throw new Error(
      'This deployment keeps nothing durable, so there are no access records. ' +
      'It needs the postgres storage driver.');
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return r.json();
  }).then(function (d) {
    GRANTS = d.grants || [];
    counts(d.summary || {});
    render();
  }).catch(function (e) {
    say(e.message, 'err');
    $('empty').hidden = false;
    $('empty').textContent = e.message;
  });
}

load();
</script>
</html>
`
