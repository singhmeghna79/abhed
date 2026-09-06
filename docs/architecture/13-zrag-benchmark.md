# Titan on the Paver zRAG benchmark

Status: 2026-09-06 · model `gemma4:26b` via Ollama, 32k context

Paver Pulse defines a benchmark for the zRAG skill: 25 IBM Z questions with
ground-truth answers and stated pass criteria. Titan runs the same skill, so the
benchmark works as a head-to-head yardstick — and it is Paver's own, which
removes any argument about the measure being chosen to flatter Titan.

**This is one side of that comparison.** The `paver` CLI is not installed on the
development machine and could not be obtained, so there is no Paver column here.
Nothing in this document says how Paver scores; anyone reporting these numbers
must say the same.

## Result

| | |
|---|---|
| passed | **20 / 25 (80%)** |
| mean turns | 3.7 |
| mean input tokens | 33,872 |
| compactions | 6 across 25 tasks |

Every failure is one of two behaviours, and neither is a crash:

| criterion | failures |
|---|---|
| no inline `[1]` citation | 4 |
| leaked tooling into the answer | 1 |

Nothing failed on an empty answer, an error leak, or a terminal error. All 25
tasks completed normally.

## What the failures are

**Four missing citations.** The answers are complete, correct-looking prose that
simply carries no `[N]` markers. The skill is emphatic — "CITATION RULES
(mandatory)" — and a 26B model follows it most of the time.

The measurement that matters here is not the 80%. It is that **the same tasks
pass and fail across runs**: `zrag-0001` and `zrag-0002` passed on an earlier
pass of this corpus and failed on this one, same model, same questions, same
corpus. A fixed seed does not help, and it is configured and verified on the
wire: a multi-turn agent run is not reproducible from a seed, because each turn's
prompt is built from the previous turn's tool results, so any divergence
compounds.

Citation compliance is therefore a probability, not a property. That is the
argument for moving citation assembly into code — building the reference list
from the documents actually retrieved and cited, rather than asking the model to
remember to do it.

**One leaked command.** `zrag-0022` wrote the `run.sh` invocation into its answer
instead of executing it, leaving the user holding a script fragment where the
references should be. SKILL.md warns against exactly this, twice, in bold. The
model did it anyway. It is the same failure class as the citations: an
instruction that is clear, and followed unreliably.

## What the run confirmed about the harness

Every harness fix made today was validated here against a real model rather than
only in tests.

| Fix | Evidence in this run |
|---|---|
| Empty-turn stall (`5e7f518`) | `zrag-0014` went from 2 turns with an empty answer to 4 turns and a real one |
| Error-leak assertion (`d655405`) | `zrag-0015` no longer fails for using the word "exception" |
| Compaction on agent sessions (`25e3fd9`) | `zrag-0008` ran 6 turns and 69,131 cumulative tokens with 2 compactions, and completed |
| Corpus loader (`d09993a`) | no phantom task in any batch |

The compaction result is the most load-bearing. Before that fix, compaction had
never once fired in a real session, and `zrag-0008` would have failed when a
request exceeded the window. It now fires on actual pressure and not otherwise:
`zrag-0019` reached 54,919 cumulative tokens with zero compactions, because no
single request came close to the limit. Cumulative tokens are not the trigger,
and should not be.

## How the criteria were translated

The benchmark states its criteria in prose. Four are decidable and became
assertions; the rest are semantic and were left out rather than approximated:

| Benchmark criterion | Assertion |
|---|---|
| "Response must not be empty" | matches `\S` |
| "Response should include inline citation markers like [1]" | matches `\[\s*\d+` |
| "Response should not mention scripts, curl, or the retrieval mechanism" | must not match `run\.sh|zrag_client|```bash` |
| "Response should not contain error messages" | must not match a traceback header, stack frame, error envelope, or non-zero exit |
| "Response should explain JES2 versus JES3" (and similar) | **not implemented** — deciding whether an answer explains something needs a judge, and Titan's evaluation refuses to let a model grade a model |

That last row is a real limitation and cuts both ways: Titan's 80% counts only
the mechanical criteria, so it is neither inflated by a lenient judge nor
penalised by a harsh one. A semantically wrong answer carrying `[1]` markers
would pass here. Paver's LLM-as-judge grader would catch that; Titan's harness
deliberately cannot.

## Reproducing

```bash
cd .titan-workspace
titan eval -corpus ../internal/eval/corpus -json report.json
```

Write the report outside the corpus directory: the loader reads every `.json`
there, and a report left beside the tasks used to be parsed back in as an empty
task.
