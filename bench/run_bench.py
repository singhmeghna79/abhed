#!/usr/bin/env python3
"""Orchestrate the Abhed vs aider vs bare-agent benchmark.

Runs 24 exercism tasks x 3 systems, sequentially, against the same local
Ollama model. Writes bench/results/<date>/<system>/<slug>.json per run plus a
combined results.json / results.csv.

Designed to be started under nohup and left to run for hours; safe to resume
(skips a task/system pair whose result JSON already exists) unless --force.
"""
import argparse
import csv
import json
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

BENCH_DIR = Path(__file__).resolve().parent
SCRATCH = Path(os.environ.get("BENCH_SCRATCH", Path(__file__).resolve().parent / ".scratch"))
TASKS_DIR = SCRATCH / "tasks"
ORIG_TESTS_DIR = SCRATCH / "orig_tests"
ABHED_CONFIG_TEMPLATE = SCRATCH / "abhed-config-template" / ".abhed"
ABHED_BIN = SCRATCH / "bin" / "abhed"
VENV_PY = SCRATCH / "venv" / "bin" / "python"
AIDER_BIN = SCRATCH / "venv" / "bin" / "aider"
SHIM_DIR = SCRATCH / "shim"

RUN_TIMEOUT_SEC = 12 * 60  # 12 minutes hard kill
PYTEST_SCORE_TIMEOUT_SEC = 60

SLUGS = [
    "leap", "hamming", "isogram", "raindrops", "bob", "acronym", "pangram",
    "anagram", "word-count", "rna-transcription", "series", "sieve", "luhn",
    "matrix", "binary-search", "run-length-encoding", "roman-numerals",
    "allergies", "clock", "robot-simulator", "phone-number",
    "protein-translation", "tournament", "grade-school",
]

SYSTEMS = ["abhed", "aider", "bare"]


def module_name(slug: str) -> str:
    return slug.replace("-", "_")


def prompt_for(slug: str) -> str:
    mod = module_name(slug)
    return (
        f"Read INSTRUCTIONS.md and implement {mod}.py so that the tests in "
        f"{mod}_test.py pass. Run `python3 -m pytest -q` to check, and fix "
        f"failures. Do not modify the test file."
    )


def fresh_workspace(slug: str, run_dir: Path) -> Path:
    mod = module_name(slug)
    ws = run_dir / "workspace"
    if ws.exists():
        shutil.rmtree(ws)
    ws.mkdir(parents=True)
    src = TASKS_DIR / slug
    for name in (f"{mod}.py", f"{mod}_test.py", "INSTRUCTIONS.md"):
        shutil.copy2(src / name, ws / name)
    return ws


IGNORED_DIR_PARTS = {"__pycache__", ".pytest_cache", ".aider.tags.cache.v4", ".git"}
IGNORED_FILE_PREFIXES = (".aider",)


def _is_ignored(rel_path: str) -> bool:
    parts = Path(rel_path).parts
    if any(p in IGNORED_DIR_PARTS for p in parts):
        return True
    if parts and parts[0].startswith(IGNORED_FILE_PREFIXES):
        return True
    return False


def snapshot_files(ws: Path) -> dict:
    """path -> mtime+size fingerprint, to detect files touched other than <slug>.py.

    Excludes __pycache__/.pytest_cache/aider metadata: byproducts of running
    pytest or aider itself, not meaningful "the agent edited another file" signal.
    """
    out = {}
    for f in ws.rglob("*"):
        if f.is_file():
            rel = str(f.relative_to(ws))
            if _is_ignored(rel):
                continue
            st = f.stat()
            out[rel] = (st.st_mtime_ns, st.st_size)
    return out


def touched_other_files(before: dict, after: dict, allowed: set) -> list:
    touched = []
    for path, fp in after.items():
        if path in allowed:
            continue
        if path not in before or before[path] != fp:
            touched.append(path)
    for path in before:
        if path not in after and path not in allowed:
            touched.append(path)
    return sorted(set(touched))


def run_with_timeout(cmd, cwd, env, timeout_sec):
    """Run a subprocess, capturing output, killing on timeout. Returns (rc, out, err, elapsed, timed_out)."""
    start = time.time()
    try:
        proc = subprocess.run(
            cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout_sec
        )
        elapsed = time.time() - start
        return proc.returncode, proc.stdout, proc.stderr, elapsed, False
    except subprocess.TimeoutExpired as e:
        elapsed = time.time() - start
        out = e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or "")
        err = e.stderr.decode() if isinstance(e.stderr, bytes) else (e.stderr or "")
        return -9, out, err, elapsed, True


def parse_abhed_tail(output: str) -> dict:
    """Extract 'N turns · X in / Y out tokens' from abhed's final line."""
    m = re.search(r"(\d+)\s*turns?\s*·\s*([\d,]+)\s*in\s*/\s*([\d,]+)\s*out\s*tokens", output)
    if not m:
        return {"turns": None, "tokens_in": None, "tokens_out": None}
    return {
        "turns": int(m.group(1)),
        "tokens_in": int(m.group(2).replace(",", "")),
        "tokens_out": int(m.group(3).replace(",", "")),
    }


def parse_aider_tail(output: str) -> dict:
    """Extract token usage from aider's final output.

    Two formats seen in practice:
      - normal completion: 'Tokens: X sent, Y received'
      - the model got stuck in a repetition loop and never produced an edit:
        'Model ... has hit a token limit!' followed by
        'Input tokens: ~N' / 'Output tokens: ~N' (approximate, note the '~').
    The second format is a genuine failure mode (aider ran out of budget
    before the model converged on an answer), not a parsing edge case to
    paper over -- so we record it distinctly via 'hit_token_limit'.
    """
    m = re.search(r"Tokens:\s*([\d.,]+)([kK]?)\s*sent,\s*([\d.,]+)([kK]?)\s*received", output)
    if m:
        def to_num(numstr, suffix):
            v = float(numstr.replace(",", ""))
            if suffix:
                v *= 1000
            return int(v)

        return {
            "tokens_sent": to_num(m.group(1), m.group(2)),
            "tokens_received": to_num(m.group(3), m.group(4)),
            "hit_token_limit": False,
        }

    m2 = re.search(
        r"has hit a token limit.*?Input tokens:\s*~?([\d,]+).*?Output tokens:\s*~?([\d,]+)",
        output, re.S,
    )
    if m2:
        return {
            "tokens_sent": int(m2.group(1).replace(",", "")),
            "tokens_received": int(m2.group(2).replace(",", "")),
            "hit_token_limit": True,
        }

    return {"tokens_sent": None, "tokens_received": None, "hit_token_limit": False}


def score(ws: Path, slug: str) -> dict:
    """Restore the ORIGINAL test file, run pytest fresh, record pass/fail + count."""
    mod = module_name(slug)
    orig_test = ORIG_TESTS_DIR / f"{mod}_test.py"
    shutil.copy2(orig_test, ws / f"{mod}_test.py")

    env = base_env()
    rc, out, err, elapsed, timed_out = run_with_timeout(
        ["python3", "-m", "pytest", "-q"], cwd=ws, env=env, timeout_sec=PYTEST_SCORE_TIMEOUT_SEC
    )
    combined = out + err
    passed = (rc == 0) and not timed_out

    # count passing tests: pytest -q prints "N passed" (with possibly "M failed" too)
    passed_count = 0
    m = re.search(r"(\d+)\s+passed", combined)
    if m:
        passed_count = int(m.group(1))
    failed_count = 0
    m = re.search(r"(\d+)\s+failed", combined)
    if m:
        failed_count = int(m.group(1))
    error_count = 0
    m = re.search(r"(\d+)\s+error", combined)
    if m:
        error_count = int(m.group(1))

    reason = None
    if timed_out:
        reason = "timeout"
    elif not passed:
        reason = "test_failures" if (failed_count or error_count) else "nonzero_exit"

    return {
        "pass": bool(passed),
        "passed_count": passed_count,
        "failed_count": failed_count,
        "error_count": error_count,
        "exit_code": rc,
        "reason": reason,
        "pytest_output_tail": combined[-3000:],
    }


def base_env():
    import os
    env = dict(os.environ)
    env["PATH"] = f"{SHIM_DIR}:{env.get('PATH','')}"
    return env


def run_abhed(slug: str, run_dir: Path) -> dict:
    ws = fresh_workspace(slug, run_dir)
    abhed_dir = ws / ".abhed"
    shutil.copytree(ABHED_CONFIG_TEMPLATE, abhed_dir)

    mod = module_name(slug)
    allowed_files = {f"{mod}.py", f"{mod}_test.py", "INSTRUCTIONS.md"}
    before = snapshot_files(ws)

    cmd = [
        str(ABHED_BIN), "-C", str(ws),
        "-mode", "accept-edits",
        "-allow", "bash(python3 -m pytest*)",
        "-max-turns", "30",
        "-p", prompt_for(slug),
    ]
    env = base_env()
    rc, out, err, elapsed, timed_out = run_with_timeout(cmd, cwd=ws, env=env, timeout_sec=RUN_TIMEOUT_SEC)
    combined = out + err
    tail_info = parse_abhed_tail(combined)
    after = snapshot_files(ws)
    touched = touched_other_files(before, after, allowed_files | {".abhed"})
    touched = [t for t in touched if not t.startswith(".abhed")]

    run_info = {
        "system": "abhed",
        "slug": slug,
        "exit_code": rc,
        "timed_out": timed_out,
        "wall_time_sec": elapsed,
        "turns": tail_info["turns"],
        "tokens_in": tail_info["tokens_in"],
        "tokens_out": tail_info["tokens_out"],
        "tokens_total": (tail_info["tokens_in"] or 0) + (tail_info["tokens_out"] or 0)
                        if tail_info["tokens_in"] is not None else None,
        "touched_other_files": touched,
        "stdout_tail": combined[-4000:],
    }

    if timed_out:
        run_info["score"] = {
            "pass": False, "passed_count": 0, "failed_count": None, "error_count": None,
            "exit_code": None, "reason": "timeout", "pytest_output_tail": "",
        }
    else:
        run_info["score"] = score(ws, slug)
    return run_info


def run_aider(slug: str, run_dir: Path) -> dict:
    ws = fresh_workspace(slug, run_dir)
    mod = module_name(slug)
    allowed_files = {f"{mod}.py", f"{mod}_test.py", "INSTRUCTIONS.md"}
    before = snapshot_files(ws)

    cmd = [
        str(AIDER_BIN),
        "--model", "ollama_chat/gemma4:26b",
        "--yes-always", "--no-auto-commits", "--no-git",
        "--read", "INSTRUCTIONS.md",
        "--read", f"{mod}_test.py",
        "--message", prompt_for(slug),
        f"{mod}.py",
    ]
    env = base_env()
    env["OLLAMA_API_BASE"] = "http://127.0.0.1:11434"
    rc, out, err, elapsed, timed_out = run_with_timeout(cmd, cwd=ws, env=env, timeout_sec=RUN_TIMEOUT_SEC)
    combined = out + err
    tok_info = parse_aider_tail(combined)
    after = snapshot_files(ws)
    touched = touched_other_files(before, after, allowed_files)
    # aider drops its own history files; don't count those against it
    touched = [t for t in touched if not t.startswith(".aider")]

    run_info = {
        "system": "aider",
        "slug": slug,
        "exit_code": rc,
        "timed_out": timed_out,
        "wall_time_sec": elapsed,
        "tokens_sent": tok_info["tokens_sent"],
        "tokens_received": tok_info["tokens_received"],
        "tokens_total": (tok_info["tokens_sent"] or 0) + (tok_info["tokens_received"] or 0)
                        if tok_info["tokens_sent"] is not None else None,
        "hit_token_limit": tok_info.get("hit_token_limit", False),
        "touched_other_files": touched,
        "stdout_tail": combined[-4000:],
    }

    if timed_out:
        run_info["score"] = {
            "pass": False, "passed_count": 0, "failed_count": None, "error_count": None,
            "exit_code": None, "reason": "timeout", "pytest_output_tail": "",
        }
    else:
        # aider does not run pytest itself per spec -- we score independently
        run_info["score"] = score(ws, slug)
    return run_info


def run_bare(slug: str, run_dir: Path) -> dict:
    ws = fresh_workspace(slug, run_dir)
    mod = module_name(slug)
    allowed_files = {f"{mod}.py", f"{mod}_test.py", "INSTRUCTIONS.md"}
    before = snapshot_files(ws)

    out_json = run_dir / "bare_agent_out.json"
    cmd = [
        str(VENV_PY), str(BENCH_DIR / "bare_agent.py"),
        "--workspace", str(ws),
        "--prompt", prompt_for(slug),
        "--max-turns", "30",
        "--out", str(out_json),
    ]
    env = base_env()
    rc, out, err, elapsed, timed_out = run_with_timeout(cmd, cwd=ws, env=env, timeout_sec=RUN_TIMEOUT_SEC)
    combined = out + err
    after = snapshot_files(ws)
    touched = touched_other_files(before, after, allowed_files)

    agent_stats = {}
    if out_json.exists():
        try:
            agent_stats = json.loads(out_json.read_text())
        except Exception:
            pass

    run_info = {
        "system": "bare",
        "slug": slug,
        "exit_code": rc,
        "timed_out": timed_out,
        "wall_time_sec": elapsed,
        "turns": agent_stats.get("turns"),
        "tokens_in": agent_stats.get("prompt_tokens"),
        "tokens_out": agent_stats.get("completion_tokens"),
        "tokens_total": agent_stats.get("total_tokens"),
        "touched_other_files": touched,
        "stdout_tail": combined[-4000:],
    }

    if timed_out:
        run_info["score"] = {
            "pass": False, "passed_count": 0, "failed_count": None, "error_count": None,
            "exit_code": None, "reason": "timeout", "pytest_output_tail": "",
        }
    else:
        run_info["score"] = score(ws, slug)
    return run_info


RUNNERS = {"abhed": run_abhed, "aider": run_aider, "bare": run_bare}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--date", required=True, help="results date dir, e.g. 2026-09-14")
    ap.add_argument("--force", action="store_true", help="re-run even if result json exists")
    ap.add_argument("--only-system", default=None, choices=SYSTEMS)
    ap.add_argument("--only-slug", default=None)
    args = ap.parse_args()

    results_root = BENCH_DIR / "results" / args.date
    runs_root = SCRATCH / "runs" / args.date

    all_results = []
    systems = [args.only_system] if args.only_system else SYSTEMS
    slugs = [args.only_slug] if args.only_slug else SLUGS

    total = len(systems) * len(slugs)
    done = 0
    for system in systems:
        sys_dir = results_root / system
        sys_dir.mkdir(parents=True, exist_ok=True)
        for slug in slugs:
            done += 1
            out_path = sys_dir / f"{slug}.json"
            if out_path.exists() and not args.force:
                print(f"[{done}/{total}] SKIP {system}/{slug} (already done)", flush=True)
                all_results.append(json.loads(out_path.read_text()))
                continue

            run_dir = runs_root / system / slug
            run_dir.mkdir(parents=True, exist_ok=True)

            print(f"[{done}/{total}] RUN {system}/{slug} ...", flush=True)
            t0 = time.time()
            try:
                info = RUNNERS[system](slug, run_dir)
            except Exception as e:
                info = {
                    "system": system, "slug": slug, "exit_code": None,
                    "timed_out": False, "wall_time_sec": time.time() - t0,
                    "error": str(e),
                    "score": {"pass": False, "passed_count": 0, "failed_count": None,
                              "error_count": None, "exit_code": None, "reason": f"harness_error: {e}",
                              "pytest_output_tail": ""},
                    "touched_other_files": [],
                }
            out_path.write_text(json.dumps(info, indent=2))
            status = "PASS" if info["score"]["pass"] else f"FAIL ({info['score'].get('reason')})"
            print(f"    -> {status} in {info['wall_time_sec']:.1f}s", flush=True)
            all_results.append(info)

    combined_path = results_root / "results.json"
    combined_path.write_text(json.dumps(all_results, indent=2))

    csv_path = results_root / "results.csv"
    with open(csv_path, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow([
            "system", "slug", "pass", "reason", "wall_time_sec", "turns",
            "tokens_total", "exit_code", "timed_out", "touched_other_files",
        ])
        for r in all_results:
            w.writerow([
                r.get("system"), r.get("slug"), r.get("score", {}).get("pass"),
                r.get("score", {}).get("reason"), round(r.get("wall_time_sec", 0), 2),
                r.get("turns"), r.get("tokens_total"), r.get("exit_code"),
                r.get("timed_out"), ";".join(r.get("touched_other_files", [])),
            ])

    print(f"\nWrote {combined_path}")
    print(f"Wrote {csv_path}")


if __name__ == "__main__":
    main()
