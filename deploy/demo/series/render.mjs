// Renders one video of the series, frame by frame, from player.html.
//
//   node render.mjs <videoId> <outdir> [--fps 30] [--preview 2]
//
// Reads <outdir>/<videoId>.timeline.json (beats with start/dur, written by
// build.sh from the narration audio) and <outdir>/<videoId>.audio.wav, drives
// the page with __seek(t) for every frame, and pipes JPEG frames into ffmpeg
// with the audio. --preview N writes a PNG every N seconds instead, for a look
// before the full render.
import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const [id, OUT] = process.argv.slice(2);
if (!id || !OUT) { console.error('usage: render.mjs <videoId> <outdir> [--fps N] [--preview S]'); process.exit(2); }
const arg = (k, d) => { const i = process.argv.indexOf(k); return i > 0 ? Number(process.argv[i + 1]) : d; };
const FPS = arg('--fps', 30), PREVIEW = arg('--preview', 0);
const HERE = path.dirname(new URL(import.meta.url).pathname);
const beats = JSON.parse(fs.readFileSync(path.join(OUT, `${id}.timeline.json`), 'utf8'));
const assetsFile = path.join(OUT, 'assets.json');
const assets = fs.existsSync(assetsFile) ? JSON.parse(fs.readFileSync(assetsFile, 'utf8')) : {};

const browser = await chromium.launch({ channel: 'chrome', headless: true, args: ['--autoplay-policy=no-user-gesture-required'] });
const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1, colorScheme: 'dark' });
const page = await ctx.newPage();
page.on('pageerror', (e) => console.error('page error:', e.message));
await page.goto('file://' + path.join(HERE, 'player.html'), { waitUntil: 'networkidle' });
await page.addScriptTag({ path: path.join(HERE, 'videos', `${id}.js`) });
const duration = await page.evaluate(([id, beats, assets]) => window.__load(id, beats, assets), [id, beats, assets]);
console.log(`${id}: ${duration.toFixed(1)}s at ${FPS}fps`);

if (PREVIEW) {
  const dir = path.join(OUT, 'preview', id); fs.mkdirSync(dir, { recursive: true });
  for (let t = 0; t < duration; t += PREVIEW) {
    await page.evaluate((t) => window.__seek(t), t);
    await page.screenshot({ path: path.join(dir, `t${String(Math.round(t * 10)).padStart(5, '0')}.png`), type: 'png' });
  }
  console.log('preview frames in', dir);
  await browser.close(); process.exit(0);
}

const frames = Math.ceil(duration * FPS);
const outFile = path.join(OUT, `${id}.mp4`);
const audio = path.join(OUT, `${id}.audio.wav`);
const ff = spawn('ffmpeg', ['-y', '-v', 'error', '-f', 'image2pipe', '-framerate', String(FPS), '-c:v', 'mjpeg', '-i', '-',
  '-i', audio, '-map', '0:v', '-map', '1:a', '-c:v', 'libx264', '-preset', 'medium', '-crf', '20', '-pix_fmt', 'yuv420p',
  '-c:a', 'aac', '-b:a', '128k', '-ar', '44100', '-shortest', '-movflags', '+faststart', outFile], { stdio: ['pipe', 'inherit', 'inherit'] });
const t0 = Date.now();
for (let i = 0; i < frames; i++) {
  const t = i / FPS;
  await page.evaluate((t) => window.__seek(t), t);
  const buf = await page.screenshot({ type: 'jpeg', quality: 92 });
  if (!ff.stdin.write(buf)) await new Promise((r) => ff.stdin.once('drain', r));
  if (i % (FPS * 10) === 0) console.log(`  ${t.toFixed(0)}s / ${duration.toFixed(0)}s  (${((Date.now() - t0) / 1000).toFixed(0)}s elapsed)`);
}
ff.stdin.end();
await new Promise((res, rej) => ff.on('close', (c) => (c === 0 ? res() : rej(new Error('ffmpeg exit ' + c)))));
await browser.close();
console.log('wrote', outFile);
