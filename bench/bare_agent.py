#!/usr/bin/env python3
"""A minimal tool-using agent loop against an OpenAI-compatible /v1/chat/completions
endpoint (Ollama). No system prompt beyond one sentence, no context compaction,
no permission policy -- this is the "bare minimum harness" baseline for the
Titan benchmark. ~150 lines by design.

Usage:
  bare_agent.py --workspace DIR --prompt TEXT [--base-url URL] [--model NAME]
                 [--max-turns N] [--out results.json]
"""
import argparse
import json
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

SYSTEM_PROMPT = "You are a coding agent with file and test tools; use them to complete the user's task."

TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "read_file",
            "description": "Read a file's contents from the workspace.",
            "parameters": {
                "type": "object",
                "properties": {"path": {"type": "string", "description": "relative path in the workspace"}},
                "required": ["path"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "write_file",
            "description": "Write (overwrite) a file's contents in the workspace.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string"},
                    "content": {"type": "string"},
                },
                "required": ["path", "content"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "list_files",
            "description": "List files in the workspace (relative paths).",
            "parameters": {"type": "object", "properties": {}, "required": []},
        },
    },
    {
        "type": "function",
        "function": {
            "name": "run_pytest",
            "description": "Run `python3 -m pytest -q` in the workspace with a 60s timeout.",
            "parameters": {"type": "object", "properties": {}, "required": []},
        },
    },
]


def call_tool(workspace: Path, name: str, args: dict) -> str:
    try:
        if name == "read_file":
            p = (workspace / args["path"]).resolve()
            if not str(p).startswith(str(workspace.resolve())):
                return "error: path escapes workspace"
            return p.read_text()
        elif name == "write_file":
            p = (workspace / args["path"]).resolve()
            if not str(p).startswith(str(workspace.resolve())):
                return "error: path escapes workspace"
            p.write_text(args["content"])
            return f"wrote {len(args['content'])} bytes to {args['path']}"
        elif name == "list_files":
            return "\n".join(
                str(f.relative_to(workspace)) for f in sorted(workspace.rglob("*")) if f.is_file()
            )
        elif name == "run_pytest":
            try:
                proc = subprocess.run(
                    ["python3", "-m", "pytest", "-q"],
                    cwd=workspace,
                    capture_output=True,
                    text=True,
                    timeout=60,
                )
                return f"exit={proc.returncode}\n{proc.stdout}\n{proc.stderr}"[:8000]
            except subprocess.TimeoutExpired:
                return "error: pytest timed out after 60s"
        else:
            return f"error: unknown tool {name}"
    except Exception as e:
        return f"error: {e}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--workspace", required=True)
    ap.add_argument("--prompt", required=True)
    ap.add_argument("--base-url", default="http://127.0.0.1:11434/v1")
    ap.add_argument("--model", default="gemma4:26b")
    ap.add_argument("--max-turns", type=int, default=30)
    ap.add_argument("--out", default=None)
    args = ap.parse_args()

    workspace = Path(args.workspace).resolve()
    messages = [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": args.prompt},
    ]

    total_prompt_tokens = 0
    total_completion_tokens = 0
    turns = 0
    final_answer = None
    start = time.time()

    for turn in range(1, args.max_turns + 1):
        turns = turn
        body = json.dumps(
            {"model": args.model, "messages": messages, "tools": TOOLS, "max_tokens": 4096}
        ).encode()
        req = urllib.request.Request(
            f"{args.base_url}/chat/completions",
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(req, timeout=180) as resp:
                data = json.loads(resp.read())
        except Exception as e:
            final_answer = f"error calling model: {e}"
            break

        usage = data.get("usage", {})
        total_prompt_tokens += usage.get("prompt_tokens", 0)
        total_completion_tokens += usage.get("completion_tokens", 0)

        choice = data["choices"][0]
        msg = choice["message"]
        messages.append(msg)

        tool_calls = msg.get("tool_calls") or []
        if not tool_calls:
            final_answer = msg.get("content", "")
            break

        for tc in tool_calls:
            fn = tc["function"]["name"]
            try:
                fn_args = json.loads(tc["function"].get("arguments") or "{}")
            except json.JSONDecodeError:
                fn_args = {}
            result = call_tool(workspace, fn, fn_args)
            messages.append(
                {
                    "role": "tool",
                    "tool_call_id": tc.get("id", ""),
                    "content": result,
                }
            )
    else:
        final_answer = final_answer or "max turns reached"

    wall_time = time.time() - start
    result = {
        "turns": turns,
        "wall_time_sec": wall_time,
        "prompt_tokens": total_prompt_tokens,
        "completion_tokens": total_completion_tokens,
        "total_tokens": total_prompt_tokens + total_completion_tokens,
        "final_answer": final_answer,
    }
    print(json.dumps(result, indent=2))
    if args.out:
        Path(args.out).write_text(json.dumps(result, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
