#!/usr/bin/env python3
"""Fail CI on gosec findings that are not already triaged.

gosec reports 136 findings against this repository, every one of them read
and triaged in docs/trust/security-scans.md (false positive, accepted with
reason, or fixed). Failing the build on that known set would make the job
red forever and teach everyone to ignore it. Failing on nothing would make
the job decorative. This gate does the useful thing in between: it compares
the findings gosec reports now with the baseline of triaged findings, keyed
by rule and file, and fails only when a rule appears in a file where it was
not triaged, or appears there more often than before.

    gosec -fmt json -no-fail -out gosec.json ./...
    python3 scripts/ci/gosec-gate.py gosec.json            # check
    python3 scripts/ci/gosec-gate.py gosec.json --update   # after triaging

--update rewrites the baseline from the current report. Only do that after
adding the new findings to docs/trust/security-scans.md; the baseline is the
list of what has been read, not a place to hide what has not.
"""
import collections
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
BASELINE = os.path.join(HERE, "gosec-baseline.json")


def load(path):
    with open(path, encoding="utf-8") as f:
        issues = json.load(f).get("Issues") or []
    counts = collections.Counter()
    for i in issues:
        rel = os.path.relpath(i["file"], os.getcwd()) if os.path.isabs(i["file"]) else i["file"]
        counts[f'{i["rule_id"]} {rel}'] += 1
    return counts


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    now = load(sys.argv[1])
    if "--update" in sys.argv:
        with open(BASELINE, "w", encoding="utf-8") as f:
            json.dump(dict(sorted(now.items())), f, indent=1)
            f.write("\n")
        print(f"baseline rewritten: {sum(now.values())} findings in {len(now)} rule/file pairs")
        return 0
    with open(BASELINE, encoding="utf-8") as f:
        base = json.load(f)
    new = {k: v - base.get(k, 0) for k, v in now.items() if v > base.get(k, 0)}
    gone = {k: base[k] - now.get(k, 0) for k in base if now.get(k, 0) < base[k]}
    if gone:
        print(f"{sum(gone.values())} triaged finding(s) no longer reported; run --update once the fix is documented:")
        for k, v in sorted(gone.items()):
            print(f"  -{v}  {k}")
    if new:
        print(f"FAIL: {sum(new.values())} gosec finding(s) not in the triaged baseline:")
        for k, v in sorted(new.items()):
            print(f"  +{v}  {k}")
        print("Triage them in docs/trust/security-scans.md, then: python3 scripts/ci/gosec-gate.py gosec.json --update")
        return 1
    print(f"ok: {sum(now.values())} gosec findings, all in the triaged baseline")
    return 0


if __name__ == "__main__":
    sys.exit(main())
