# Observability

Every session is already an ordered log of events — that is the audit trail,
and it is the source of truth. Observability is the same facts in the shape
your tracing backend expects: one trace per session, a span per tool call and
per subagent, with the numbers an operator asks for attached as attributes.

Titan speaks OTLP/HTTP directly. There is no SDK to install and no vendor in
the path; anything that accepts OpenTelemetry traces — an OTel Collector,
Jaeger, Tempo, Honeycomb, Datadog — works.

## Turn it on

```json
"telemetry": {
  "enabled": true,
  "endpoint": "http://otel-collector:4318",
  "service_name": "titan",
  "headers": { "Authorization": "Bearer …" }
}
```

`titan serve` prints `telemetry <endpoint>` at startup when it is active. The
exporter posts to `<endpoint>/v1/traces`.

## What a trace contains

| Span | Parent | Attributes |
|---|---|---|
| `titan.session` | — | `titan.session.id`, `titan.model`, `titan.mode`, and at the end `titan.turns`, `titan.tokens.in`, `titan.tokens.out`, `titan.tokens.cached`, `titan.compactions`, `titan.reason` |
| `tool.<name>` | the session | `titan.tool`, `titan.call.id`, `titan.tool.args` (when under 2 KB), `titan.tool.duration_ms`, `titan.tool.exit_code`, `titan.tool.truncated` |
| `titan.subagent` | the parent session | as `titan.session`, plus `titan.session.parent` |

A tool call the policy engine **refused** is still a span: error status, with
`titan.denied = true` and the reason. That is deliberately the most visible
thing in a trace, because a refusal is the event a security review most wants
to find.

Compactions and subagent spawns appear as span events on the session.

## Properties worth knowing

**It cannot slow a session.** The exporter is a tap on the event store, not a
stage in the pipeline. `Append` hands it the event and returns; the export
happens on its own goroutine with its own timeout. If the collector is down,
spans are dropped and counted — a missing span is a smaller failure than a
stalled agent, and `Dropped` and `Failed` counters say how much of the picture
is missing.

**Trace IDs are deterministic.** The trace ID is a hash of the session ID and
each span ID is a hash of its call ID, so the same session always produces the
same trace. Replay a session from the event log and you get an identical trace
to put beside the original — which is exactly what an auditor comparing the two
wants, and something a generated ID cannot give you.

**A subagent joins its parent's trace.** Delegated work is visible from the
run that delegated it rather than starting a trace of its own.

**A session cut off by a restart is visible as exactly that.** Spans still
open at shutdown are closed with `titan.unfinished = true` rather than
discarded.

## What it is not

It is not a metrics or logs exporter — traces only, because that is the shape
an agent run naturally has. And it is not a substitute for the event log: the
log is complete and append-only; a trace is a view of it that can lose spans
when the network does. If the two disagree, the log is right.
