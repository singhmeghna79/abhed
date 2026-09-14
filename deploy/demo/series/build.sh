#!/usr/bin/env bash
# Build the Abhed video series.
#
#   ./deploy/demo/series/build.sh [outdir] [video ids...]      full render
#   PREVIEW=2 ./deploy/demo/series/build.sh [outdir] [ids...]  a PNG every 2s
#
# For each video: narrate every beat of videos/<id>.script.json with a neural
# US-English voice, measure the audio, lay the beats on a timeline with a short
# breath between them, then render the picture from that timeline. The picture
# is a function of the timeline, so it is aligned with the voice by
# construction — there is nothing to sync afterwards.
set -euo pipefail
# A render runs at about twice realtime, long enough for the Mac to sleep, and
# a sleeping Mac stalls Chrome on a frame until the screenshot times out.
# Keep the machine awake for exactly as long as this script runs.
if command -v caffeinate >/dev/null 2>&1 && [ -z "${DEMO_AWAKE:-}" ]; then
  DEMO_AWAKE=1 exec caffeinate -dims "$0" "$@"
fi
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
OUT="${1:-$ROOT/.series-build}"; shift || true
IDS=("$@"); [ ${#IDS[@]} -gt 0 ] || IDS=(01-zybuu 02-abhed 03-install 04-using)
PY="${DEMO_PY:-python3}"
VOICE="${DEMO_VOICE:-en-US-AriaNeural}"
GAP="${DEMO_GAP:-0.55}"
LEAD="${DEMO_LEAD:-1.2}"
mkdir -p "$OUT/tts"
for t in ffmpeg ffprobe node; do command -v "$t" >/dev/null || { echo "error: $t not found" >&2; exit 1; }; done

if [ -n "${DEMO_NODE_MODULES:-}" ]; then ln -sfn "$DEMO_NODE_MODULES" "$HERE/node_modules"; fi
[ -d "$HERE/node_modules/playwright" ] || ( cd "$HERE" && npm install --no-save --no-package-lock --silent playwright@1.47.2 )

# Captured CLI transcripts (deploy/demo/series/cli/*.txt or $OUT/cli/*.txt)
# ride along in assets.json, so the scenes show real output, never typed-in.
"$PY" - "$OUT" "$HERE" <<'PY'
import json, os, sys, glob
out, here = sys.argv[1], sys.argv[2]
p = os.path.join(out, 'assets.json'); a = json.load(open(p)) if os.path.exists(p) else {}
for d in (os.path.join(out, 'cli'), os.path.join(here, 'cli')):
    for f in glob.glob(os.path.join(d, '*.txt')):
        k = os.path.basename(f)[:-4]; txt = open(f, errors='replace').read()
        lines = [l for l in txt.split('\n') if not l.startswith('EXIT ')]
        a[k] = '\n'.join(lines)
        if k == 'run1':
            # the first run splits into the tool calls and the refusal + report
            cut = next((i for i, l in enumerate(lines) if 'rejected' in l), len(lines))
            a['run1_head'] = '\n'.join(lines[:cut]); a['run1_tail'] = '\n'.join(lines[cut-1:])
json.dump(a, open(p, 'w'), indent=1)
PY

for id in "${IDS[@]}"; do
  echo "== $id"
  "$PY" - "$HERE/videos/$id.script.json" "$OUT" "$id" "$VOICE" "$GAP" "$LEAD" <<'PY'
import asyncio, json, os, subprocess, sys
script, out, vid, voice, gap, lead = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], float(sys.argv[5]), float(sys.argv[6])
beats = json.load(open(script))
import edge_tts
async def go():
    for b in beats:
        p = os.path.join(out, 'tts', f"{vid}-{b['id']}.mp3")
        if not os.path.exists(p):
            await edge_tts.Communicate(b['text'], voice, rate=b.get('rate', '-3%')).save(p)
asyncio.run(go())
def dur(p): return float(subprocess.check_output(['ffprobe','-v','error','-show_entries','format=duration','-of','csv=p=0',p]).decode().strip())
t = lead; tl = []; parts = []
for b in beats:
    mp3 = os.path.join(out, 'tts', f"{vid}-{b['id']}.mp3"); wav = mp3[:-4] + '.wav'
    if not os.path.exists(wav):
        subprocess.check_call(['ffmpeg','-y','-v','error','-i',mp3,'-ar','44100','-ac','1',wav])
    d = dur(wav)
    hold = float(b.get('hold', 0))            # extra silence after this beat, for a picture that needs it
    tl.append({'id': b['id'], 'start': round(t, 3), 'dur': round(d, 3), 'text': b['text']})
    parts.append((wav, t)); t += d + gap + hold
json.dump(tl, open(os.path.join(out, f'{vid}.timeline.json'), 'w'), indent=1)
# one audio track: silence, with every beat placed at its start
total = t + 1.5
inputs = []; filt = []
for i, (wav, start) in enumerate(parts):
    inputs += ['-i', wav]; filt.append(f"[{i}:a]adelay={int(start*1000)}|{int(start*1000)}[a{i}]")
mix = ''.join(f"[a{i}]" for i in range(len(parts))) + f"amix=inputs={len(parts)}:normalize=0,apad=whole_dur={total:.2f}[out]"
subprocess.check_call(['ffmpeg','-y','-v','error'] + inputs + ['-filter_complex', ';'.join(filt) + ';' + mix, '-map', '[out]', '-ar', '44100', '-ac', '1', os.path.join(out, f'{vid}.audio.wav')])
print('  ' + ' '.join(f"{b['id']}@{b['start']:.1f}s" for b in tl) + f"  total {total:.0f}s")
PY
  if [ -n "${PREVIEW:-}" ]; then
    ( cd "$HERE" && node render.mjs "$id" "$OUT" --preview "$PREVIEW" )
  else
    ( cd "$HERE" && node render.mjs "$id" "$OUT" --fps "${DEMO_FPS:-30}" )
    mkdir -p "$ROOT/web/zybuu/demo/series"
    cp "$OUT/$id.mp4" "$ROOT/web/zybuu/demo/series/$id.mp4"
    echo "  -> web/zybuu/demo/series/$id.mp4 ($(du -h "$OUT/$id.mp4" | cut -f1))"
    # The announcement film is public, on the homepage; the rest stay gated.
    if [ "$id" = "01-zybuu" ]; then
      mkdir -p "$ROOT/web/zybuu/media"
      cp "$OUT/$id.mp4" "$ROOT/web/zybuu/media/zybuu-announcement.mp4"
      ffmpeg -y -v error -ss 9.5 -i "$OUT/$id.mp4" -frames:v 1 -q:v 3 "$ROOT/web/zybuu/media/zybuu-announcement.jpg"
      echo "  -> web/zybuu/media/zybuu-announcement.mp4 (public, on the homepage)"
    fi
  fi
done
