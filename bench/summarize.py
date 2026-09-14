#!/usr/bin/env python3
"""Summarize bench/results/<date>/results.json into per-system and per-task tables.
Prints Markdown tables suitable for pasting into RESULTS.md.
"""
import argparse
import json
import statistics as st
from pathlib import Path

BENCH_DIR = Path(__file__).resolve().parent
SYSTEMS = ["abhed", "aider", "bare"]


def mean(xs):
    xs = [x for x in xs if x is not None]
    return sum(xs) / len(xs) if xs else None


def fmt(x, nd=1):
    if x is None:
        return "-"
    return f"{x:.{nd}f}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--date", required=True)
    args = ap.parse_args()

    results_path = BENCH_DIR / "results" / args.date / "results.json"
    results = json.loads(results_path.read_text())

    by_system = {s: [r for r in results if r["system"] == s] for s in SYSTEMS}

    print("## Per-system summary\n")
    print("| System | Pass rate | Mean wall time (s) | Mean tokens | Mean turns | Tasks touching other files |")
    print("|---|---|---|---|---|---|")
    for s in SYSTEMS:
        rows = by_system[s]
        n = len(rows)
        passed = sum(1 for r in rows if r.get("score", {}).get("pass"))
        wall = mean([r.get("wall_time_sec") for r in rows])
        tokens = mean([r.get("tokens_total") for r in rows])
        turns = mean([r.get("turns") for r in rows])
        touched = sum(1 for r in rows if r.get("touched_other_files"))
        print(f"| {s} | {passed}/{n} ({100*passed/n:.0f}%) | {fmt(wall)} | {fmt(tokens,0)} | {fmt(turns,1)} | {touched}/{n} |")

    print("\n## Per-task detail\n")
    slugs = sorted({r["slug"] for r in results})
    header = "| Task | " + " | ".join(f"{s} pass" for s in SYSTEMS) + " | " + " | ".join(f"{s} time(s)" for s in SYSTEMS) + " |"
    print(header)
    print("|" + "---|" * (1 + 2 * len(SYSTEMS)))
    lookup = {(r["system"], r["slug"]): r for r in results}
    for slug in slugs:
        cells = []
        times = []
        for s in SYSTEMS:
            r = lookup.get((s, slug))
            if r is None:
                cells.append("?")
                times.append("-")
            else:
                p = r.get("score", {}).get("pass")
                reason = r.get("score", {}).get("reason")
                cells.append("PASS" if p else f"FAIL({reason})" if reason else "FAIL")
                times.append(fmt(r.get("wall_time_sec")))
        print(f"| {slug} | " + " | ".join(cells) + " | " + " | ".join(times) + " |")


if __name__ == "__main__":
    main()
