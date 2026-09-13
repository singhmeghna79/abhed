// Captures real console footage for video 4 with Playwright against the
// running deployment, and converts each clip to H.264 so the player can seek
// it frame by frame. Writes <outdir>/footage/*.mp4 and <outdir>/assets.json.
//
//   UXUSER=demo UXPASS=… CONSOLE=http://127.0.0.1:8080 node footage.mjs <outdir>
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const OUT = process.argv[2]; if (!OUT) { console.error('usage: footage.mjs <outdir>'); process.exit(2); }
const CONSOLE = process.env.CONSOLE || 'http://127.0.0.1:8080';
const USER = process.env.UXUSER, PASS = process.env.UXPASS;
const SIZE = { width: 1600, height: 900 };
const dir = path.join(OUT, 'footage'); fs.mkdirSync(dir, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const assets = fs.existsSync(path.join(OUT, 'assets.json')) ? JSON.parse(fs.readFileSync(path.join(OUT, 'assets.json'), 'utf8')) : {};

async function clip(name, run) {
  const raw = path.join(dir, 'raw-' + name); fs.rmSync(raw, { recursive: true, force: true });
  const ctx = await browser.newContext({ viewport: SIZE, colorScheme: 'dark', recordVideo: { dir: raw, size: SIZE } });
  const page = await ctx.newPage();
  const t0 = Date.now();
  try { await run(page, ctx); } catch (e) { console.error(name, 'error:', e.message); }
  const v = page.video(); await ctx.close();
  const webm = await v.path(); const mp4 = path.join(dir, name + '.mp4');
  execFileSync('ffmpeg', ['-y', '-v', 'error', '-i', webm, '-c:v', 'libx264', '-preset', 'fast', '-crf', '18', '-pix_fmt', 'yuv420p', '-g', '15', '-an', mp4]);
  fs.rmSync(raw, { recursive: true, force: true });
  assets[name] = 'file://' + mp4;
  console.log('clip', name, Math.round((Date.now() - t0) / 1000) + 's');
}
async function type(page, sel, text) { await page.click(sel); for (const ch of text) { await page.keyboard.type(ch); await sleep(20 + Math.random() * 35); } }
async function signin(page) {
  await page.goto(CONSOLE + '/'); await page.fill('.signin input[type=text]', USER); await page.fill('.signin input[type=password]', PASS);
  await page.click('.signin .btn'); await page.waitForURL('**/console', { timeout: 15000 }); await sleep(1200);
}

// 1. sign in, slowly, and land on the launchpad
await clip('signin', async (page) => {
  await page.goto(CONSOLE + '/', { waitUntil: 'networkidle' }); await sleep(1500);
  await type(page, '.signin input[type=text]', USER); await sleep(400);
  await page.fill('.signin input[type=password]', PASS); await sleep(800);
  await page.click('.signin .btn'); await page.waitForURL('**/console', { timeout: 15000 }); await sleep(3500);
});

// 2. a read-only question in plan mode: tool calls appear as they happen
await clip('ask', async (page) => {
  await signin(page); await page.selectOption('#mode', 'plan'); await sleep(600);
  await type(page, '#q', 'Read tempconv/tempconv.py and explain, in three sentences, what it does and what is missing.');
  await sleep(600); await page.keyboard.press('Enter'); await sleep(42000);
});

// 3. a write in default mode: approval card, approve, result; then the model picker
await clip('approve', async (page) => {
  await signin(page); await page.selectOption('#mode', 'default'); await sleep(500);
  await type(page, '#q', 'Add k_to_c(k) for Kelvin to tempconv/tempconv.py, with a docstring, in the same style as c_to_f.');
  await page.keyboard.press('Enter');
  const yes = page.locator('.approve .yes').first();
  try { await yes.waitFor({ timeout: 60000 }); await sleep(3000); await yes.hover(); await sleep(600); await yes.click(); } catch {}
  await sleep(12000);
});

// 4. attachments: pick a file, ask about it
await clip('attach', async (page) => {
  await signin(page); await page.selectOption('#mode', 'plan'); await sleep(400);
  const tmp = path.join(OUT, 'design-note.md');
  fs.writeFileSync(tmp, '# Sensor ingest — design note\n\nThe ingest service reads temperatures from field sensors over MQTT, converts units, and writes hourly aggregates to Postgres. External systems: MQTT broker (mosquitto), Postgres, Grafana for dashboards, PagerDuty for alerts.\n');
  await page.setInputFiles('#file', tmp); await sleep(1200);
  await type(page, '#q', 'Read the attached design note and list every external system it depends on.');
  await sleep(500); await page.keyboard.press('Enter'); await sleep(32000);
});

// 5. replay a finished chat, then delete one
await clip('replay', async (page) => {
  await signin(page); await sleep(800);
  const rows = page.locator('.item'); const n = await rows.count();
  if (n > 1) { await rows.nth(1).click(); await sleep(5000); }
  if (n) { const last = rows.nth(n - 1); await last.hover(); await sleep(900); await last.locator('.del').click({ force: true }); await sleep(1600); await page.locator('.item.confirm .yes').click(); await sleep(2500); }
});

// 6. the deployment page: facts, activity, tools, containment
await clip('overview', async (page) => {
  await signin(page); await page.goto(CONSOLE + '/', { waitUntil: 'networkidle' }); await sleep(2500);
  await page.evaluate(() => new Promise((res) => { let y = 0; const id = setInterval(() => { y += 6; window.scrollTo(0, y); if (y > 1300) { clearInterval(id); res(); } }, 16); }));
  await sleep(2500);
});

fs.writeFileSync(path.join(OUT, 'assets.json'), JSON.stringify(assets, null, 1));
await browser.close();
console.log('assets:', Object.keys(assets).join(' '));
