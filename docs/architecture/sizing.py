#!/usr/bin/env python3
"""Abhed capacity model. Reproduces every number in 04-sizing.md.

Standard transformer memory arithmetic - no vendor claims, no benchmarks.
    kv_per_token = 2 * layers * kv_heads * head_dim * dtype_bytes
Run: python3 sizing.py
"""

GB = 1024 ** 3

# name, total_params_B, active_params_B, layers, kv_heads, head_dim
MODELS = [
    ("Qwen3-32B (dense)",       32,  32.0, 64, 8, 128),
    ("gpt-oss-120b (MoE)",     117,   5.1, 36, 8,  64),
    ("Llama-3.3-70B (dense)",   70,  70.0, 80, 8, 128),
    ("Qwen3-235B-A22B (MoE)",  235,  22.0, 94, 4, 128),
]

# name, total_hbm_GB, usable_fraction
TIERS = [
    ("T1  2x RTX 6000 Ada",    96, 0.85),
    ("T2  8x H100 SXM",       640, 0.90),
    ("T3  8x H200 SXM",      1128, 0.90),
    ("T4  32x H100 (4 node)", 2560, 0.88),
    ("T5  64x H200 (8 node)", 9024, 0.88),
]

H100_FP8_TFLOPS = 989.0   # dense (non-sparse) per GPU


def kv_per_token_gb(layers, kv_heads, head_dim, dtype_bytes=2):
    return 2 * layers * kv_heads * head_dim * dtype_bytes / GB


def weights_gb(params_b, bits):
    return params_b * 1e9 * (bits / 8) / GB


def prefill_seconds(tokens, active_b, gpus=8, mfu=0.40):
    """Prefill is compute-bound: ~2*N*P FLOPs. Scales with ACTIVE params."""
    return (2 * active_b * 1e9 * tokens) / (H100_FP8_TFLOPS * 1e12 * gpus * mfu)


def main():
    print("=" * 78)
    print("PER-MODEL MEMORY PROFILE")
    print("=" * 78)
    print(f"{'model':26}{'W@FP8':>9}{'W@INT4':>9}{'KV/tok':>10}{'KV@128k':>10}{'KV@1M':>9}")
    for name, tot, _, L, kvh, hd in MODELS:
        k = kv_per_token_gb(L, kvh, hd)
        print(f"{name:26}{weights_gb(tot,8):8.1f}G{weights_gb(tot,4):8.1f}G"
              f"{k*1024*1024:9.0f}KB{k*131072:9.1f}G{k*1048576:8.0f}G")

    print("\n" + "=" * 78)
    print("CONCURRENT SESSIONS @ 45k live context, FP8 weights")
    print("=" * 78)
    ctx = 45000
    for tname, hbm, eff in TIERS:
        usable = hbm * eff
        for mname, tot, _, L, kvh, hd in MODELS:
            w = weights_gb(tot, 8)
            if w > usable * 0.75:
                continue
            n = int((usable - w) / (kv_per_token_gb(L, kvh, hd) * ctx))
            print(f"{tname:24}{mname:26}{n:>6} sessions")

    print("\n" + "=" * 78)
    print("PREFILL COST on 8x H100 @ 40% MFU  (scales with ACTIVE params)")
    print("=" * 78)
    for label, toks in [("system prompt + ABHED.md", 12000),
                        ("mid-session + repo context", 60000),
                        ("near compaction threshold", 190000),
                        ("20-subagent fan-out", 240000)]:
        moe = prefill_seconds(toks, 5.1)
        dense = prefill_seconds(toks, 32.0)
        print(f"{label:30}{toks:>8,} tok   MoE-120b {moe*1000:7.0f}ms   "
              f"dense-32B {dense*1000:7.0f}ms")

    print("\n" + "=" * 78)
    print("PREFIX CACHE IMPACT  (40-turn session, 60k stable prefix, Qwen3-32B)")
    print("=" * 78)
    turns, prefix = 40, 60000
    cold = prefill_seconds(prefix, 32.0) * turns
    warm = prefill_seconds(prefix, 32.0) + prefill_seconds(2000, 32.0) * (turns - 1)
    print(f"  without prefix cache : {cold:6.1f} GPU-s")
    print(f"  with prefix cache    : {warm:6.1f} GPU-s   ({cold/warm:.0f}x reduction)")
    print("  NOTE: compaction invalidates the prefix -> each event pays cold cost again.")


if __name__ == "__main__":
    main()
