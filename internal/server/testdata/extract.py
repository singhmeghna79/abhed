# Extracts render() and its helpers from console.go so the browser logic can
# be driven headlessly. See console_render_test.go.
import pathlib, re, sys
src = pathlib.Path(sys.argv[1]).read_text()

def grab(fn):
    i = src.index(fn)
    line_end = src.index('\n', i)
    first = src[i:line_end]
    if first.count('{') == first.count('}') and first.rstrip().endswith('}'):
        return first
    j = src.index('\n}\n', i) + 3
    return src[i:j]

wanted = ['function node(cls, text){','function lastStreamedBubble(){','function wordCount(s){',
          'function setCollapsed(wrap, on){','function collapse(wrap, on){','function setPeek(wrap, content){',
          'function makeCollapsible(wrap, hdr){','function clip(s, n){','function summarize(tool, args){',
          'function shortPath(p){','function kv(k, v){']
seen=set(); out=[]
for fn in wanted:
    if fn not in src: continue
    name = re.match(r'function (\w+)', fn).group(1)
    if name in seen: continue
    seen.add(name); out.append(grab(fn))
i = src.index('function render(ev){'); j = src.index('function kv(k, v){', i)
out.append(src[i:j])
sys.stdout.write('\n'.join(out))
