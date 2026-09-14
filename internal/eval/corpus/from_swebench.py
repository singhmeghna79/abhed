#!/usr/bin/env python3
"""Convert a SWE-bench Verified subset into Abhed task JSON.

SWE-bench cannot ship inside Abhed: it is an external dataset with its own
licensing, and an air-gapped bundle cannot download it. This script is the
bridge — run it once on a connected machine, commit the output, and the tasks
travel with your corpus like any other.

Usage:
    pip install datasets
    python3 from_swebench.py --limit 100 > 06-swebench.json

SCOPE, stated plainly: SWE-bench instances are repository-level tasks that need
the real repo checked out at a specific commit and its test suite run. Abhed's
corpus format seeds a small workspace and checks file assertions. This converter
therefore produces tasks that check the PATCH SHAPE (the right files touched,
the right symbols present), not full test-suite equivalence.

That is weaker than SWE-bench's own harness and should not be reported as a
SWE-bench score. It is useful for regression detection against a fixed
reference, not for comparability with published numbers. To get real
comparability, run SWE-bench's harness with Abhed as the agent.
"""
import argparse
import json
import re
import sys


def files_touched(patch: str):
    """Extract the file paths a unified diff modifies."""
    return sorted(set(re.findall(r"^\+\+\+ b/(.+)$", patch, re.M)))


def added_symbols(patch: str, limit: int = 3):
    """Pull identifiers from added lines, as a coarse 'did the fix land' signal."""
    out = []
    for line in patch.splitlines():
        if not line.startswith("+") or line.startswith("+++"):
            continue
        m = re.search(r"(?:def|func|class)\s+(\w+)", line)
        if m and m.group(1) not in out:
            out.append(m.group(1))
        if len(out) >= limit:
            break
    return out


def convert(inst):
    patch = inst.get("patch", "")
    paths = files_touched(patch)
    if not paths:
        return None

    assertions = [{"type": "file_exists", "path": p} for p in paths[:5]]
    for sym in added_symbols(patch):
        assertions.append({"type": "file_contains", "path": paths[0], "value": sym})

    return {
        "id": "swebench-" + inst["instance_id"].replace("/", "-").replace("__", "-"),
        "name": f"SWE-bench: {inst['instance_id']}",
        "category": "swebench",
        "difficulty": "hard",
        "stresses": "repository-scale navigation and editing",
        "max_turns": 80,
        "prompt": (
            inst.get("problem_statement", "").strip()
            + "\n\nThe repository is checked out in the working directory."
        ),
        # Empty: the real repo must be checked out by the runner, not seeded
        # from JSON. See the note in the module docstring.
        "files": {},
        "assertions": assertions,
        "_swebench": {
            "instance_id": inst["instance_id"],
            "repo": inst.get("repo", ""),
            "base_commit": inst.get("base_commit", ""),
            "note": "checkout required before running; assertions check patch shape only",
        },
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--split", default="test", help="dataset split")
    ap.add_argument("--dataset", default="princeton-nlp/SWE-bench_Verified")
    ap.add_argument("--limit", type=int, default=100)
    args = ap.parse_args()

    try:
        from datasets import load_dataset
    except ImportError:
        sys.exit("pip install datasets first — this needs network access, "
                 "so run it outside the enclave and commit the output")

    ds = load_dataset(args.dataset, split=args.split)
    tasks = []
    for inst in ds:
        task = convert(inst)
        if task:
            tasks.append(task)
        if len(tasks) >= args.limit:
            break

    print(json.dumps(tasks, indent=2))
    print(f"\n// {len(tasks)} tasks; each needs its repo checked out at base_commit",
          file=sys.stderr)


if __name__ == "__main__":
    main()
