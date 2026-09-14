#!/usr/bin/env python3
"""Inline deploy/sitegen/site.css into the first <style> block of each page.

The marketing pages are single files on purpose — they are served with
`default-src 'none'` and read raw by internal/sitecheck — so the shared
stylesheet is copied in rather than linked. This script is the only thing
that should write that block. Run at publish time by deploy/publish-site.sh
and by hand after editing site.css; `--check` fails if a page is stale, which
is what the site tests call so a hand edit to the copy cannot ship.
"""
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
PAGES = [
    os.path.join(ROOT, "web", "zybuu", "index.html"),
    os.path.join(ROOT, "web", "zybuu", "abhed", "index.html"),
]
BLOCK = re.compile(r"<style data-shared>.*?</style>", re.S)


def render(page, css):
    src = open(page, encoding="utf-8").read()
    if not BLOCK.search(src):
        sys.exit(f"{page}: no <style data-shared> block to fill")
    return src, BLOCK.sub(lambda _: "<style data-shared>\n" + css + "</style>", src, count=1)


def main():
    css = open(os.path.join(HERE, "site.css"), encoding="utf-8").read()
    check = "--check" in sys.argv
    stale = []
    for page in PAGES:
        before, after = render(page, css)
        if before == after:
            continue
        if check:
            stale.append(page)
        else:
            open(page, "w", encoding="utf-8").write(after)
            print(f"inlined site.css into {os.path.relpath(page, ROOT)}")
    if stale:
        sys.exit("stale shared stylesheet in: " + ", ".join(os.path.relpath(p, ROOT) for p in stale)
                 + "\nrun: python3 deploy/sitegen/inline-css.py")


if __name__ == "__main__":
    main()
