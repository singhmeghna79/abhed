// Tests for the access-request Function.
//
// Run:  node deploy/sitetests/access.test.mjs
//
// The form is the homepage's only conversion, and the two behaviours that
// matter most are the ones nobody sees in a browser: an unset RESEND_API_KEY
// must refuse honestly rather than accept a request and drop it, and an
// upstream failure must report failure rather than claim success. Both are
// invisible until the day they matter, so they are asserted here.
//
// It lives OUTSIDE web/zybuu/ deliberately. Everything under functions/ is a
// route: Pages would publish this as /api/access.test, a route module that
// exports no handler. The same mistake put deploy.sh on the public site.
//
// No framework: the Function is plain web-standard JS, and Node has had
// Request/FormData for years. A dependency here would be the only one in
// this directory.

import { onRequestPost, onRequest } from '../../web/zybuu/functions/api/access.js';

const url = 'https://zybuu.com/api/access';
const req = (fields) => {
  const fd = new FormData();
  for (const [k, v] of Object.entries(fields)) fd.set(k, v);
  return new Request(url, { method: 'POST', body: fd });
};
const frag = (r) => new URL(r.headers.get('location')).hash;

let fetched = null;
globalThis.fetch = async (u, init) => { fetched = { u, init }; return { ok: true }; };

const cases = [];
const t = (name, got, want) => cases.push([name, got, want, got === want]);

// no key configured -> refuses honestly, sends nothing
fetched = null;
t('unconfigured refuses', frag(await onRequestPost({ request: req({ name: 'A', email: 'a@b.co' }), env: {} })), '#access-unconfigured');
t('unconfigured sends no mail', fetched === null, true);

const env = { RESEND_API_KEY: 'k' };
t('missing name', frag(await onRequestPost({ request: req({ email: 'a@b.co' }), env })), '#access-missing');
t('missing email', frag(await onRequestPost({ request: req({ name: 'A' }), env })), '#access-missing');
t('bad email', frag(await onRequestPost({ request: req({ name: 'A', email: 'nope' }), env })), '#access-email');

// honeypot: answered means bot -> accepted silently, nothing sent
fetched = null;
t('honeypot looks ok', frag(await onRequestPost({ request: req({ name: 'A', email: 'a@b.co', website: 'x' }), env })), '#access-ok');
t('honeypot sends no mail', fetched === null, true);

// happy path
fetched = null;
t('valid ok', frag(await onRequestPost({ request: req({ name: 'A', email: 'a@b.co', company: 'C', use: 'U' }), env })), '#access-ok');
t('valid sends mail', fetched !== null, true);
const body = JSON.parse(fetched.init.body);
t('reply_to is requester', body.reply_to, 'a@b.co');
t('to is default', body.to[0], 'support@zybuu.com');

// ACCESS_FROM override — sending as a verified domain rather than Resend's
// shared address. Getting this wrong silently sends from the wrong identity.
await onRequestPost({ request: req({ name: 'A', email: 'a@b.co' }),
                      env: { ...env, ACCESS_FROM: 'Zybuu <hello@zybuu.com>' } });
t('ACCESS_FROM honoured', JSON.parse(fetched.init.body).from, 'Zybuu <hello@zybuu.com>');
await onRequestPost({ request: req({ name: 'A', email: 'a@b.co' }), env });
t('default From when unset', JSON.parse(fetched.init.body).from, 'Zybuu <onboarding@resend.dev>');

// ACCESS_TO override
await onRequestPost({ request: req({ name: 'A', email: 'a@b.co' }), env: { ...env, ACCESS_TO: 'x@y.z' } });
t('ACCESS_TO honoured', JSON.parse(fetched.init.body).to[0], 'x@y.z');

// length cap
const long = 'z'.repeat(5000);
await onRequestPost({ request: req({ name: long, email: 'a@b.co' }), env });
t('name capped at 2000', JSON.parse(fetched.init.body).text.includes('z'.repeat(2001)), false);

// upstream failure -> reported, not swallowed as success
globalThis.fetch = async () => ({ ok: false });
t('resend failure reported', frag(await onRequestPost({ request: req({ name: 'A', email: 'a@b.co' }), env })), '#access-failed');
globalThis.fetch = async () => { throw new Error('network'); };
t('network throw reported', frag(await onRequestPost({ request: req({ name: 'A', email: 'a@b.co' }), env })), '#access-failed');

// stray GET -> back to the page, not an error document
t('GET redirects', new URL((await onRequest({ request: new Request(url) })).headers.get('location')).hash, '#access');

let bad = 0;
for (const [n, got, want, ok] of cases) {
  if (!ok) { bad++; console.log(`  FAIL ${n}: got ${JSON.stringify(got)} want ${JSON.stringify(want)}`); }
}
console.log(bad === 0 ? `  all ${cases.length} checks pass` : `  ${bad}/${cases.length} FAILED`);
process.exit(bad === 0 ? 0 : 1);
