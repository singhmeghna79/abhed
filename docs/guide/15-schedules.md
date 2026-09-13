# Scheduled runs

A recurring run is the shape of a lot of real work: every weekday at nine,
summarise what changed overnight; hourly, check the staging pods; Sunday
night, run the dependency audit. `titan serve` can run those itself.

```json
"schedules": [
  {
    "name":   "overnight-summary",
    "cron":   "0 9 * * 1-5",
    "prompt": "Summarise every commit since yesterday 18:00 and flag anything that touches auth or billing.",
    "mode":   "plan"
  },
  {
    "name":   "staging-health",
    "cron":   "@hourly",
    "prompt": "List pods in the staging namespace that restarted in the last hour and say why.",
    "mode":   "auto"
  }
]
```

A scheduled run is an ordinary session that the clock started. It is listed,
recorded and replayable like any other, and it appears as the user
`schedule:<name>`.

## The expression

Five fields — minute, hour, day of month, month, weekday — with `*`, lists
(`1,15`), ranges (`1-5`), steps (`*/15`), and the aliases `@hourly`, `@daily`,
`@weekly`, `@monthly`. Evaluated in the server's local time.

A bad expression is a **startup error**. `titan serve` refuses to start with a
schedule it could not parse, because a job that silently never fires is worse
than a server that says why it will not start.

## Nobody is there to say yes

A scheduled run has no person at the console, so an `ask` — a tool call the
mode would normally pause on — is **refused**, and recorded as a denial like
any other. If a job needs to write, give it a mode or allow rules that do not
need a person: `"mode": "auto"` approves by rule while deny rules still stand.
That is a decision made in config, on the record, not a default made for you.

`"mode": "plan"` is the safe choice for anything that only needs to read.

## No overlap

If a job is still running when its next firing comes round, that firing is
skipped and counted. Two agents doing the same nightly task at once is almost
never what anyone meant.

## The admin view

`/admin` lists every schedule with its next firing, last run, last session,
last error, and a **Run now** button. The same is available as
`GET /v1/admin/schedules` and `POST /v1/admin/schedules/<name>/run`, both
admin-only.

## What it does not do

It does not persist run history across a restart — the sessions do, in the
store, but the "last run" shown in the admin view starts fresh. It does not
catch up missed firings: if the server was down at nine, the nine o'clock run
did not happen. And it does not run jobs on a machine other than the one
serving — a job is a session on this deployment.
