// Records the Abhed demo, scene by scene, with Playwright driving the system
// Chrome. Each scene is one browser context with video recording on, held
// open for at least as long as its narration (durations.json, written by
// build.sh from the TTS files), and saved as scenes/<id>.webm. build.sh then
// lays the narration over each clip and joins them.
//
// Scenes use local files for the marketing pages and docs (what will be
// published), and the running deployment for the console, so the recording
// shows the real product and never a mock. Nothing here is typed by a person:
// re-running it after a UI change re-records the whole thing.
//
//   UXUSER=… UXPASS=… CONSOLE=http://127.0.0.1:8080 node deploy/demo/record.mjs <outdir>
import { chromium } from 'playwright';
import fs from 'node:fs';
import path from 'node:path';

const OUT = process.argv[2] || 'demo-out';
const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..', '..');
const SITE = 'file://' + path.join(ROOT, 'web', 'zybuu');
const CONSOLE = process.env.CONSOLE || 'http://127.0.0.1:8080';
const USER = process.env.UXUSER, PASS = process.env.UXPASS;
const durations = JSON.parse(fs.readFileSync(path.join(OUT, 'durations.json'), 'utf8'));
const SIZE = { width: 1280, height: 720 };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browser = await chromium.launch({ channel: 'chrome', headless: true });

// SCENES=s4,s5 re-records only those; the others keep their existing clips.
const ONLY = (process.env.SCENES || '').split(',').map((s) => s.trim()).filter(Boolean);

async function scene(id, run) {
  if (ONLY.length && !ONLY.includes(id)) return;
  const dir = path.join(OUT, 'raw', id);
  fs.rmSync(dir, { recursive: true, force: true });
  const ctx = await browser.newContext({ viewport: SIZE, colorScheme: 'dark', deviceScaleFactor: 1,
    recordVideo: { dir, size: SIZE } });
  const page = await ctx.newPage();
  const started = Date.now();
  const minMs = Math.ceil((durations[id] || 10) * 1000) + 600;
  try { await run(page, ctx); } catch (e) { console.error(id, 'scene error:', e.message); }
  const left = minMs - (Date.now() - started);
  if (left > 0) await sleep(left);
  const video = page.video();
  await ctx.close();
  const p = await video.path();
  fs.renameSync(p, path.join(OUT, 'scenes', id + '.webm'));
  console.log('recorded', id, Math.round((Date.now() - started) / 1000) + 's');
}

async function glide(page, y, ms = 1400) {
  await page.evaluate(([y, ms]) => new Promise((res) => {
    const from = window.scrollY, d = y - from, t0 = performance.now();
    const step = (t) => { const k = Math.min(1, (t - t0) / ms), e = k < .5 ? 2 * k * k : -1 + (4 - 2 * k) * k;
      window.scrollTo(0, from + d * e); if (k < 1) requestAnimationFrame(step); else res(); };
    requestAnimationFrame(step);
  }), [y, ms]);
}
async function top(page, sel) { return page.evaluate((s) => { const e = document.querySelector(s); return e ? e.getBoundingClientRect().top + window.scrollY - 70 : 0; }, sel); }
async function type(page, sel, text) {
  await page.click(sel);
  for (const ch of text) { await page.keyboard.type(ch); await sleep(18 + Math.random() * 40); }
}

fs.mkdirSync(path.join(OUT, 'scenes'), { recursive: true });

// 1 — zybuu.com hero, the terminal running.
await scene('s1', async (page) => {
  await page.goto(SITE + '/index.html', { waitUntil: 'networkidle' });
  await sleep(2000);
  await page.mouse.move(640, 400);
});

// 2 — the vision and the comparison.
await scene('s2', async (page) => {
  await page.goto(SITE + '/index.html', { waitUntil: 'networkidle' });
  await sleep(600);
  await glide(page, await top(page, '#vision'), 1600); await sleep(5200);
  await glide(page, await top(page, '#compare'), 1600); await sleep(6000);
  await glide(page, (await top(page, '#compare')) + 260, 1200);
});

// 3 — the Abhed page and the real run.
await scene('s3', async (page) => {
  await page.goto(SITE + '/abhed/index.html', { waitUntil: 'networkidle' });
  await sleep(2600);
  await glide(page, await top(page, '#replay'), 1600);
  await sleep(12000);
  await glide(page, (await top(page, '#replay')) + 220, 1400);
});

// 4 — the five guarantees, governance, limitations.
await scene('s4', async (page) => {
  await page.goto(SITE + '/abhed/index.html', { waitUntil: 'networkidle' });
  await sleep(400);
  await glide(page, await top(page, '#why'), 1400); await sleep(6500);
  await glide(page, await top(page, '#governance'), 1600); await sleep(6500);
  await glide(page, await top(page, '#limitations'), 1600); await sleep(6000);
  await glide(page, (await top(page, '#limitations')) + 320, 1400);
});

// 5 — the console sign-in.
await scene('s5', async (page) => {
  await page.goto(CONSOLE + '/', { waitUntil: 'networkidle' });
  await sleep(2500);
  await type(page, '.signin input[type=text]', USER);
  await sleep(300);
  await page.fill('.signin input[type=password]', PASS);
  await sleep(700);
  await page.click('.signin .btn');
  await page.waitForURL('**/console', { timeout: 15000 }).catch(() => {});
  await sleep(2500);
});

// 6 — a first task: read-only, inside the sandbox.
async function signin(ctx, page) {
  await page.goto(CONSOLE + '/');
  await page.fill('.signin input[type=text]', USER);
  await page.fill('.signin input[type=password]', PASS);
  await page.click('.signin .btn');
  await page.waitForURL('**/console', { timeout: 15000 }).catch(() => {});
  await sleep(1200);
}
await scene('s6', async (page, ctx) => {
  await signin(ctx, page);
  await page.selectOption('#mode', 'plan');
  await type(page, '#q', 'List the files in the workspace and tell me, in two sentences, what this project is.');
  await sleep(500);
  await page.keyboard.press('Enter');
  await sleep(26000);
});

// 7 — a write waits for a person; then a chat is deleted.
await scene('s7', async (page, ctx) => {
  await signin(ctx, page);
  await page.selectOption('#mode', 'default');
  await type(page, '#q', 'Create NOTES.md containing one line that names this project.');
  await page.keyboard.press('Enter');
  // Wait for the approval card, approve it, then let the run finish.
  const card = page.locator('.approve .yes').first();
  try { await card.waitFor({ timeout: 45000 }); await sleep(2500); await card.click(); } catch {}
  await sleep(7000);
  // Delete the oldest chat from the rail.
  const rows = page.locator('.item');
  const n = await rows.count();
  if (n) {
    const row = rows.nth(n - 1);
    await row.hover(); await sleep(900);
    await row.locator('.del').click({ force: true }); await sleep(1400);
    await page.locator('.item.confirm .yes').click(); await sleep(2500);
  }
});

// 8 — access is issued, not self-served: the policy the dashboard enforces.
// The dashboard itself is the operator's, and the recording is made with a
// throwaway account that is deliberately not an administrator.
await scene('s8', async (page) => {
  await page.goto(SITE + '/abhed/access-policy.html', { waitUntil: 'networkidle' }).catch(() => {});
  await sleep(4500);
  await glide(page, 520, 1600); await sleep(5000);
  await glide(page, 1100, 1600); await sleep(4000);
});

// 9 — the documentation.
await scene('s9', async (page) => {
  const docs = SITE + '/docs/index.html';
  await page.goto(docs, { waitUntil: 'networkidle' });
  await sleep(3500);
  await glide(page, 500, 1600); await sleep(3000);
  await page.goto(SITE + '/docs/guide/13-structured-output.html', { waitUntil: 'networkidle' }).catch(() => {});
  await sleep(4000);
});

// 10 — back to zybuu.com, the request.
await scene('s10', async (page) => {
  await page.goto(SITE + '/index.html', { waitUntil: 'networkidle' });
  await sleep(300);
  await glide(page, await top(page, '#contact'), 1800);
  await sleep(6000);
});

await browser.close();
console.log('done');
