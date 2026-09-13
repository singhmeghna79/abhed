#!/usr/bin/env python3
"""Render docs/ into web/zybuu/docs/ as static HTML.

The documentation is written as markdown in docs/ because that is where it is
useful to whoever is editing the code. Duplicating it into HTML by hand would
guarantee the two drift, so this renders it at publish time instead: the
markdown stays the source, the site is a build artifact.

Deliberately dependency-free. A docs build that needs a package install is a
docs build that breaks on a machine that has not run it before, and this has to
work from the same laptop that serves the site.
"""
import html
import os
import re
import shutil
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SRC = os.path.join(ROOT, "docs")
OUT = os.path.join(ROOT, "web", "zybuu", "docs")
# The same HTML is embedded in the binary, so an air-gapped install has local
# documentation with no route to the public copy. One generator, two outputs —
# rendering twice from one source is what keeps them from disagreeing.
EMBED = os.path.join(ROOT, "internal", "docsite", "site")

# Which trees are published. docs/internal/ is competitive analysis and working
# notes — it stays off the public site.
SECTIONS = [
    ("guide", "Guide", "Using Titan day to day."),
    ("architecture", "Architecture", "How it is built, and why."),
    ("ops", "Operations", "Running it in a real environment."),
]


def slug(section, name):
    # No .html extension. Cloudflare Pages serves /a/b for /a/b.html and 308s
    # the extension away, so linking to the extension costs every reader a
    # redirect on every click. The files are still written as .html.
    return f"{section}/{re.sub(r'\.md$', '', name)}"


def title_of(text, fallback):
    m = re.search(r"^#\s+(.+)$", text, re.M)
    return m.group(1).strip() if m else fallback


# --- a small, strict markdown subset --------------------------------------
# Only what the docs actually use. A partial renderer that is honest about its
# scope beats a permissive one that silently mangles something.
def render(md, section):
    out, i, lines = [], 0, md.split("\n")
    while i < len(lines):
        ln = lines[i]

        # fenced code
        if ln.startswith("```"):
            lang = ln[3:].strip()
            body, i = [], i + 1
            while i < len(lines) and not lines[i].startswith("```"):
                body.append(lines[i]); i += 1
            i += 1
            out.append('<pre class="code"><code class="lang-%s">%s</code></pre>'
                       % (html.escape(lang), html.escape("\n".join(body))))
            continue

        # table
        if ln.startswith("|") and i + 1 < len(lines) and re.match(r"^\|[\s:|-]+\|$", lines[i + 1]):
            head = [c.strip() for c in ln.strip("|").split("|")]
            i += 2
            rows = []
            while i < len(lines) and lines[i].startswith("|"):
                rows.append([c.strip() for c in lines[i].strip("|").split("|")])
                i += 1
            t = ["<div class='tw'><table><thead><tr>"]
            t += ["<th>%s</th>" % inline(c, section) for c in head]
            t.append("</tr></thead><tbody>")
            for r in rows:
                t.append("<tr>" + "".join("<td>%s</td>" % inline(c, section) for c in r) + "</tr>")
            t.append("</tbody></table></div>")
            out.append("".join(t))
            continue

        # heading
        m = re.match(r"^(#{1,4})\s+(.+)$", ln)
        if m:
            lvl = len(m.group(1))
            txt = m.group(2).strip()
            anchor = re.sub(r"[^a-z0-9]+", "-", txt.lower()).strip("-")
            out.append('<h%d id="%s">%s</h%d>' % (lvl, anchor, inline(txt, section), lvl))
            i += 1
            continue

        # list
        if re.match(r"^\s*[-*]\s+", ln):
            items = []
            while i < len(lines) and re.match(r"^\s*[-*]\s+", lines[i]):
                items.append(re.sub(r"^\s*[-*]\s+", "", lines[i])); i += 1
            out.append("<ul>" + "".join("<li>%s</li>" % inline(x, section) for x in items) + "</ul>")
            continue
        if re.match(r"^\s*\d+\.\s+", ln):
            items = []
            while i < len(lines) and re.match(r"^\s*\d+\.\s+", lines[i]):
                items.append(re.sub(r"^\s*\d+\.\s+", "", lines[i])); i += 1
            out.append("<ol>" + "".join("<li>%s</li>" % inline(x, section) for x in items) + "</ol>")
            continue

        # blockquote
        if ln.startswith(">"):
            body = []
            while i < len(lines) and lines[i].startswith(">"):
                body.append(lines[i].lstrip("> ")); i += 1
            out.append("<blockquote>%s</blockquote>" % inline(" ".join(body), section))
            continue

        if ln.strip() == "---":
            out.append("<hr>"); i += 1; continue

        if not ln.strip():
            i += 1; continue

        # paragraph
        para = []
        while i < len(lines) and lines[i].strip() and not re.match(
                r"^(#{1,4}\s|```|\||\s*[-*]\s|\s*\d+\.\s|>)", lines[i]):
            para.append(lines[i]); i += 1
        if para:
            out.append("<p>%s</p>" % inline(" ".join(para), section))
    return "\n".join(out)


def inline(s, section):
    # Code spans are extracted first so their contents are never treated as
    # markup — otherwise a documented `<tag>` becomes real markup.
    spans = []

    def stash(m):
        spans.append(m.group(1))
        return "\x00%d\x00" % (len(spans) - 1)

    s = re.sub(r"`([^`]+)`", stash, s)
    s = html.escape(s)
    s = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", s)
    s = re.sub(r"(?<!\*)\*([^*]+)\*(?!\*)", r"<em>\1</em>", s)

    def link(m):
        text, href = m.group(1), m.group(2)
        if href.endswith(".md"):
            href = re.sub(r"\.md$", "", href)
        elif ".md#" in href:
            href = href.replace(".md#", "#")
        return '<a href="%s">%s</a>' % (href, text)

    s = re.sub(r"\[([^\]]+)\]\(([^)]+)\)", link, s)
    for n, c in enumerate(spans):
        s = s.replace("\x00%d\x00" % n, "<code>%s</code>" % html.escape(c))
    return s


def main():
    style = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "docs.css")).read()
    logo = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "logo.html")).read()

    if os.path.isdir(OUT):
        shutil.rmtree(OUT)
    os.makedirs(OUT)

    # Collect pages
    tree = []
    for sec, label, blurb in SECTIONS:
        d = os.path.join(SRC, sec)
        if not os.path.isdir(d):
            continue
        pages = []
        for name in sorted(os.listdir(d)):
            if not name.endswith(".md") or name == "README.md":
                continue
            text = open(os.path.join(d, name)).read()
            pages.append((name, title_of(text, name), text))
        tree.append((sec, label, blurb, pages))

    total = sum(len(p[3]) for p in tree)
    if total == 0:
        print("no documentation found under docs/ — refusing to write an empty site",
              file=sys.stderr)
        return 1

    def nav(cur_sec, cur_name):
        n = ['<nav class="side"><a class="side-home" href="/docs/">Documentation</a>']
        for sec, label, blurb, pages in tree:
            n.append('<div class="sgrp"><p class="slabel">%s</p><ul>' % label)
            for name, title, _ in pages:
                on = ' class="on"' if (sec == cur_sec and name == cur_name) else ""
                n.append('<li><a%s href="/docs/%s">%s</a></li>' % (on, slug(sec, name), html.escape(title)))
            n.append("</ul></div>")
        n.append("</nav>")
        return "".join(n)

    def shell(title, body, cur_sec="", cur_name="", desc=""):
        return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(title)} — Titan documentation</title>
<meta name="description" content="{html.escape(desc)}">
<link rel="icon" href="data:image/svg+xml,%3Csvg%20xmlns=%22http://www.w3.org/2000/svg%22%20viewBox=%220%200%20256%20256%22%3E%20%3Cdefs%3E%20%3ClinearGradient%20id=%22zt%22%20x1=%220%22%20y1=%220%22%20x2=%221%22%20y2=%221%22%3E%20%3Cstop%20offset=%220%25%22%20stop-color=%22%235CC4FF%22/%3E%20%3Cstop%20offset=%2250%25%22%20stop-color=%22%232A8CF0%22/%3E%20%3Cstop%20offset=%22100%25%22%20stop-color=%22%230B3C8C%22/%3E%20%3C/linearGradient%3E%20%3CradialGradient%20id=%22zl%22%20cx=%2222%25%22%20cy=%2218%25%22%20r=%2280%25%22%3E%20%3Cstop%20offset=%220%25%22%20stop-color=%22%23FFFFFF%22%20stop-opacity=%22.26%22/%3E%20%3Cstop%20offset=%2260%25%22%20stop-color=%22%23FFFFFF%22%20stop-opacity=%220%22/%3E%20%3C/radialGradient%3E%20%3Cmask%20id=%22zcut%22%3E%20%3Crect%20width=%22256%22%20height=%22256%22%20fill=%22%23fff%22/%3E%20%3Cg%20fill=%22%23000%22%3E%20%3Crect%20x=%2270%22%20y=%2262%22%20width=%22126%22%20height=%2234%22%20rx=%229%22/%3E%20%3Crect%20x=%2260%22%20y=%22160%22%20width=%22126%22%20height=%2234%22%20rx=%229%22/%3E%20%3Cpath%20d=%22M156%2096%20H200%20L100%20160%20H56%20Z%22/%3E%20%3C/g%3E%20%3C/mask%3E%20%3C/defs%3E%20%3Crect%20x=%2214%22%20y=%2214%22%20width=%22228%22%20height=%22228%22%20rx=%2258%22%20fill=%22url(%23zt)%22%20mask=%22url(%23zcut)%22/%3E%20%3Crect%20x=%2214%22%20y=%2214%22%20width=%22228%22%20height=%22228%22%20rx=%2258%22%20fill=%22url(%23zl)%22%20mask=%22url(%23zcut)%22/%3E%20%3C/svg%3E">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600&display=swap">
<style>{style}</style>
</head>
<header>
  <div class="dwrap bar">
    {logo}
    <nav class="nav">
      <a href="https://zybuu.com/" class="hide-sm">Zybuu</a>
      <a href="https://zybuu.com/titan/">Titan</a>
      <a href="/docs/">Docs</a>
      <a class="btn" href="https://titan.zybuu.com">Open console</a>
    </nav>
  </div>
</header>
<div class="dwrap layout">
{nav(cur_sec, cur_name)}
<main class="doc">
{body}
</main>
</div>
<footer><div class="dwrap foot">
  <span><a href="https://zybuu.com/">Zybuu</a></span><span><a href="https://zybuu.com/titan/">Titan</a></span>
  <span>Documentation is generated from docs/ in the repository</span>
</div></footer>
</html>
"""

    written = 0
    for sec, label, blurb, pages in tree:
        os.makedirs(os.path.join(OUT, sec), exist_ok=True)
        for name, title, text in pages:
            body = render(text, sec)
            p = os.path.join(OUT, sec, re.sub(r"\.md$", ".html", name))
            open(p, "w").write(shell(title, body, sec, name, blurb))
            written += 1

    # Index
    idx = ['<h1>Titan documentation</h1>',
           '<p class="lede">Titan is a deep agent harness. It runs where your code is, '
           'against whichever model you point it at, and records everything it does.</p>']
    for sec, label, blurb, pages in tree:
        idx.append('<h2 id="%s">%s</h2><p>%s</p><div class="cards">' % (sec, label, blurb))
        for name, title, text in pages:
            first = ""
            for para in text.split("\n\n"):
                p = para.strip()
                if p and not p.startswith("#") and not p.startswith("|"):
                    first = " ".join(p.split())[:150]
                    break
            idx.append('<a class="dcard" href="/docs/%s"><b>%s</b><span>%s</span></a>'
                       % (slug(sec, name), html.escape(title), html.escape(first)))
        idx.append("</div>")
    open(os.path.join(OUT, "index.html"), "w").write(
        shell("Titan documentation", "".join(idx), desc="Documentation for Titan, the Zybuu agent harness."))
    written += 1

    # Mirror into the binary's embed directory.
    if os.path.isdir(EMBED):
        for name in os.listdir(EMBED):
            if name == ".keep":
                continue
            q = os.path.join(EMBED, name)
            shutil.rmtree(q) if os.path.isdir(q) else os.remove(q)
    else:
        os.makedirs(EMBED)
    for name in os.listdir(OUT):
        src_p, dst_p = os.path.join(OUT, name), os.path.join(EMBED, name)
        shutil.copytree(src_p, dst_p) if os.path.isdir(src_p) else shutil.copy2(src_p, dst_p)

    print("  rendered %d pages from docs/" % written)
    print("  mirrored into internal/docsite/site for the embedded copy")
    return 0


if __name__ == "__main__":
    sys.exit(main())
