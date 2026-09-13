#!/usr/bin/env bash
# Build the recorded demo: narration (TTS), screen recording, and the join.
#
#   UXUSER=<console user> UXPASS=<password> ./deploy/demo/build.sh [outdir]
#
# Produces web/zybuu/demo/titan-demo.mp4 and poster.jpg (gitignored; published
# with the site behind the signed-link gate) from deploy/demo/narration.json
# and deploy/demo/record.mjs. Needs: ffmpeg, node with playwright (and system
# Chrome), and a Python with edge-tts. The console user is a throwaway account
# on the local deployment; nothing in the recording is a real customer.
#
# Voice: en-US-AriaNeural, a neural US-English voice. It is a synthesised
# narration, and the demo page says so nowhere because it does not need to —
# the words are ours; only the reading is a machine's.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
OUT="${1:-$ROOT/.demo-build}"
PY="${DEMO_PY:-python3}"
VOICE="${DEMO_VOICE:-en-US-AriaNeural}"
mkdir -p "$OUT/tts" "$OUT/scenes" "$OUT/mux"

for t in ffmpeg ffprobe node; do
  command -v "$t" >/dev/null || { echo "error: $t not found" >&2; exit 1; }
done
: "${UXUSER:?set UXUSER to a console account for the recording}"
: "${UXPASS:?set UXPASS}"

# ---- 1. narration
echo "== narration ($VOICE)"
"$PY" - "$HERE/narration.json" "$OUT/tts" "$VOICE" <<'PY'
import asyncio, json, sys, os
import edge_tts
scenes = json.load(open(sys.argv[1])); out = sys.argv[2]; voice = sys.argv[3]
async def go():
    for sc in scenes:
        p = os.path.join(out, sc['id'] + '.mp3')
        if os.path.exists(p): continue
        await edge_tts.Communicate(sc['text'], voice, rate='-4%').save(p)
asyncio.run(go())
PY
"$PY" - "$HERE/narration.json" "$OUT" <<'PY'
import json, subprocess, sys, os
scenes = json.load(open(sys.argv[1])); out = sys.argv[2]; d = {}
for sc in scenes:
    p = os.path.join(out, 'tts', sc['id'] + '.mp3')
    d[sc['id']] = float(subprocess.check_output(['ffprobe','-v','error','-show_entries','format=duration','-of','csv=p=0', p]).decode().strip())
json.dump(d, open(os.path.join(out, 'durations.json'), 'w'), indent=1)
print(' '.join(f"{k}={v:.1f}s" for k, v in d.items()))
PY

# ---- 2. recording
#
# ES modules do not honour NODE_PATH, so playwright has to be resolvable from
# deploy/demo itself. Point DEMO_NODE_MODULES at a directory that already has
# it (a scratch install) and it is linked in; otherwise it is installed here,
# without touching a package.json, and the directory is gitignored.
echo "== recording"
if [ -n "${DEMO_NODE_MODULES:-}" ]; then
  ln -sfn "$DEMO_NODE_MODULES" "$HERE/node_modules"
elif [ ! -d "$HERE/node_modules/playwright" ]; then
  ( cd "$HERE" && npm install --no-save --no-package-lock --silent playwright@1.47.2 )
fi
# Video recording needs Playwright's own ffmpeg build, fetched once into its
# browser cache; the system Chrome is used for the browser itself.
( cd "$HERE" && npx --no-install playwright install ffmpeg >/dev/null 2>&1 || true )
( cd "$HERE" && node "$HERE/record.mjs" "$OUT" )

# ---- 3. lay narration over each scene, pad the shorter side, join
echo "== mux"
LIST="$OUT/mux/list.txt"; : > "$LIST"
for f in $("$PY" -c "import json,sys;print(' '.join(s['id'] for s in json.load(open(sys.argv[1]))))" "$HERE/narration.json"); do
  V="$OUT/scenes/$f.webm"; A="$OUT/tts/$f.mp3"; M="$OUT/mux/$f.mp4"
  dv="$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$V")"
  da="$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$A")"
  # Video must be at least as long as the narration; audio at least as long
  # as the video. Whichever is shorter is padded, so nothing is cut.
  ffmpeg -y -v error -i "$V" -i "$A" \
    -filter_complex "[0:v]scale=1280:720:flags=lanczos,fps=30,tpad=stop_mode=clone:stop_duration=$(awk -v a="$da" -v v="$dv" 'BEGIN{d=a-v+0.4; print (d>0)?d:0}')[v];[1:a]apad=pad_dur=$(awk -v a="$da" -v v="$dv" 'BEGIN{d=v-a+0.4; print (d>0)?d:0}')[a]" \
    -map "[v]" -map "[a]" -shortest \
    -c:v libx264 -preset medium -crf 27 -pix_fmt yuv420p -c:a aac -b:a 96k -ar 44100 "$M"
  echo "file '$M'" >> "$LIST"
  echo "  $f  video ${dv%.*}s  audio ${da%.*}s"
done
FINAL="$ROOT/web/zybuu/demo/titan-demo.mp4"
ffmpeg -y -v error -f concat -safe 0 -i "$LIST" -c copy -movflags +faststart "$FINAL"
ffmpeg -y -v error -ss 3 -i "$FINAL" -frames:v 1 -q:v 3 "$ROOT/web/zybuu/demo/poster.jpg"
SZ=$(du -h "$FINAL" | cut -f1); DUR=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$FINAL")
echo "== built $FINAL ($SZ, ${DUR%.*}s)"
# Cloudflare Pages serves files up to 25 MiB; say so before the publish fails.
if [ "$(stat -f%z "$FINAL" 2>/dev/null || stat -c%s "$FINAL")" -gt 26214400 ]; then
  echo "warning: over 25 MiB; raise -crf in this script or shorten the narration" >&2
fi
