# do-something

A local CLI for one list of projects and media. It ranks activities by
value, commitment, interest, and their fit right now. Pure Go; no service or account.

## Example usage

Imagine keeping shows, small chores, and weekend projects in the same list.
The following walkthrough follows one person's changing plans. Ratings and
runtimes are illustrative; types and categories such as `tv` and `course`
are optional labels, not built-in.

### Media tracking & suggestions

Say you're currently (`--status in_progress`) watching Breaking Bad
and Friends.

```sh
$ do-something add --title "Breaking Bad" --kind media --type tv \
  --total-duration 60h --remaining-duration 10h --min-session-duration 50m \
  --intensity high --quality max --actuality none --interest max \
  --status in_progress

$ do-something add --title "Friends" --kind media --type tv \
  --total-duration 90h --remaining-duration 50m --min-session-duration 25m \
  --intensity none --quality low --influence low --actuality none --interest medium \
  --status in_progress
```

Perhaps you feel like watching TV (`--kind media --type tv`), but have
only one hour to do it (`--session 1h`).

```sh
$ do-something suggest --kind media --type tv --session 1h --limit 1
 0.68  01a10673-1  Breaking Bad  tv  10h · ~83.33% complete  [in_progress]  strong interest · estimated commitment · estimated value
```

Breaking Bad can be a bit heavy (`--intensity high`), so if you're
in the mood for something lighter you can add a filter (`--effort low`).

```sh
$ do-something suggest --kind media --type tv --session 1h --effort low --limit 1
 0.55  01a10673-3  Friends  tv  50m · ~99.07% complete  [in_progress]  low commitment · some interest · already in progress
```

### Progress & completion

You can also track progress.

```sh
$ do-something log Friends
Logged 25m. Remaining: 50m → 25m. Status unchanged. ~99.54% complete

$ do-something show Friends
 0.55  01a10673-3  Friends  tv  25m · ~99.54% complete  [in_progress]  low commitment · some interest · already in progress
```

Default log duration uses the stored minimum session, which was set
to 25 minutes for Friends. (To keep things generic, there are no
parameters for things like “episodes“ or “seasons“.)

“Can I finish something in half an hour?” is a different question
from “can I spend half an hour on it?”; here, the task must be able
to be fully completed within some time.

```sh
$ do-something suggest --kind media --type tv --finish-within 30m --effort low
 0.55  01a10673-3  Friends  tv  25m · ~99.54% complete  [in_progress]  low commitment · some interest · already in progress

$ do-something log Friends
Remaining time is zero. Mark this item done? [y/N] y
Logged 25m. Remaining: 25m → 0m. Marked done. ~100.00% complete

$ do-something list --status done
 0.46  01a10673-3  Friends  tv  0m · ~100.00% complete  [done]  low commitment · some interest · estimated value
```

### Projects & cost

Add a walk and a purchase you need for a future project. Session time and money
are separate constraints: ordering materials is quick, but it is not free.

```sh
$ do-something add --title "Evening walk" --kind project --type exercise \
  --ongoing --min-session-duration 15m \
  --modes movement --engagements loose --intensity low \
  --physical high --interest high --cost 0 \
  --status in_progress

$ do-something add --title "Order materials" --kind project --type errand \
  --total-duration 10m --remaining-duration 10m --min-session-duration 10m \
  --modes thinking --engagements loose --intensity low --interest low --cost 100

$ do-something suggest --kind both --session 30m --effort low --budget 0 \
  --unknown exclude --mode movement --engagement loose
 0.62  01a10675-6  Evening walk  exercise  ongoing  [in_progress·movement,loose]  some interest · estimated value · unknown commitment

$ do-something log "Evening walk" 20m
Logged 20m. Ongoing activity; status unchanged.
```

### Prerequisites & modes

Before building a bookshelf, you want to finish a video course and buy the
materials. A prerequisite can be a different kind of item.

```sh
$ do-something add --title "Woodworking course" --kind project --type course \
  --total-duration 2h --remaining-duration 2h --min-session-duration 30m \
  --modes thinking --engagements focused --intensity medium \
  --mental medium --interest high

$ do-something add --title "Build a bookshelf" --kind project --type build \
  --total-duration 8h --remaining-duration 8h --min-session-duration 2h \
  --modes hands_on,making --engagements focused --intensity medium --interest high \
  --cost 20 --depends-on "Woodworking course,Order materials"

$ do-something suggest --type build --session 3h
no items match

$ do-something deps list "Build a bookshelf"
Dependencies for "Build a bookshelf" [01a10676-5]:
  "Order materials" [01a10675-8]
  "Woodworking course" [01a10676-3]
```

The suggestion is empty: the bookshelf is blocked by both prerequisites.

You finish the course first. The bookshelf is still blocked by the purchase:

```sh
$ do-something start "Woodworking course"
"Woodworking course" [01a10676-3]: not_started → in_progress

$ do-something log "Woodworking course" 2h --complete
Logged 2h. Remaining: 2h → 0m. Marked done. ~100.00% complete

$ do-something suggest --type build --session 3h --include-unready --peek
 0.67  01a10676-5  Build a bookshelf  build  8h · ~0.00% complete  [not_started·hands_on,making,focused]  not suggested recently · some interest · estimated commitment
  blocked by "Order materials" [01a10675-8] (not_started)
```

`--include-unready` reveals the bookshelf with its remaining blocker. You decide
to buy the materials, then ask again with a small budget for the building work:

```sh
$ do-something start "Order materials"
"Order materials" [01a10675-8]: not_started → in_progress

$ do-something log "Order materials" 10m --cost 100 --complete
Logged 10m. Remaining: 10m → 0m. Marked done. ~100.00% complete
Logged cost 100.00. Cost left: 100.00 → 0.00.

$ do-something suggest --type build --session 3h --budget 25 --unknown exclude
 0.67  01a10676-5  Build a bookshelf  build  8h · ~0.00% complete  [not_started·hands_on,making,focused]  not suggested recently · some interest · estimated commitment

do-something start "Build a bookshelf"
"Build a bookshelf" [01a10676-5]: not_started → in_progress
```

### Revising the plan

Maybe the shelves turn out to need more work than expected.

```sh
$ do-something edit "Build a bookshelf" --total-duration 12h --remaining-duration 10h
```

Perhaps you're no longer enjoying the drama enough to continue. Remove it
from suggestions without deleting its record. You can later change your mind.

```sh
$ do-something drop "Breaking Bad"
"Breaking Bad" [01a10673-1]: in_progress → dropped

$ do-something list --status dropped
 0.58  01a10673-1  Breaking Bad  tv  10h · ~83.33% complete  [dropped]  strong interest · estimated commitment · estimated value

$ do-something reopen "Breaking Bad"
"Breaking Bad" [01a10673-1]: dropped → not_started
```

## Agentic usage

The application has been designed ground-up with agentic use in mind.

You:
   > I've got about an hour on the couch. What should I watch?

Agent:
   > **Breaking Bad** — the engine puts it on top (0.68 vs Friends' 0.55): it's the show you're actually into (interest and quality at max), and with 10h left, an hour on the couch is a full episode deep into it, not a throwaway.
   >
   > **Friends** is the lighter call: 50m left, so that one hour finishes it completely — and you like seeing things through. It ranks lower mostly because it's a comfort show (quality 0.33, interest 0.5) and was already suggested recently, so its cooldown term is at zero.
   >
   > So: feel for the demanding one → **Breaking Bad**; if tonight is more "settle in and let go" → **Friends**.

## What it is in more words

This is a **personal "what should I do next?" CLI — a to-do list that decides
for you** — written in Go and backed by a SQLite-like store, with multi-device
sync. It manages two kinds of things — **projects** (things you do) and
**media** (things you consume) — and its job is to rank everything in your
backlog against your *current situation* and tell you what to do right now.

**The core idea is `suggest`, the default command.** You describe your state
— `--session`/`--time` (time available), `--finish-within`, `--budget`,
`--effort` (none→max, mapped to `max_intensity`), `--mode`
(`movement`/`hands_on`/`thinking`/`making`), `--engagement`
(`focused`/`loose`), plus filters for kind, status, tags, min-score — and
it returns ranked items. `--random` is the "just pick something for me"
dice mode, `--peek` is "show me without committing" mode, and `--explain`
exposes the full scoring math.

**A transparent, explainable scoring model.** `score_breakdown` has two halves:
- `base`: weighted groups of nine 0–1 ratings — `intensity, career, physical,
  mental, social, interest, actuality, influence, quality` — a life-balance
  model (wellbeing, career, social, curiosity...). Crucially each rating
  carries `rated_at` + `age_months`, so **stale ratings decay** — the system
  knows your opinions go out of date.
- `right_now`: terms with `days_left` (deadline urgency), `pace` (are you on
  pace to finish in time?), `ready`/`total` (dependency completion), and
  `anchor_age`. So urgency isn't hardcoded; it's a live function of deadlines,
  remaining duration, and dependency readiness.

Items also carry `estimated_progress_percent`, `reasons` (which factors boosted
it), and `filter_evidence` (why it passed/failed a filter) — designed so both
humans and scripts can audit every recommendation.

**A real lifecycle and time/cost tracking.** Statuses `not_started →
in_progress → done | dropped` map to `start/done/drop/reopen`. `log <id>
--time-passed 2h` records an event with cost/remaining before-and-after,
auto-completes items when their remaining duration runs out (`--complete`,
ongoing items keep `remaining_after: null`), and tracks both *time* and
*money* (costs, budgets). Dependencies form a checked DAG (`CYCLE` error
code); items blocked by unfinished deps are "unready" unless
`--include-unready`.

**A nice epistemic layer.** Every property has a `property_state` of `known`
/ `unknown` / `not_applicable`, and `--unknown include|exclude` lets you
decide how to treat items you haven't fully rated — the app is honest about
what it doesn't know, instead of silently assuming zero.

**Multi-device sync via a plain directory** (`--syncdir`): device identity
(`device_name`, `database_uuid`), snapshots with digests, archives, a
three-way merge (base/local/remote per table+key), and explicit resolution
(`--take-remote`, `--keep-local`, `--merge`, `--master`). `doctor` does
health checks, lock inspection, and GC of event history.

**Built for agents and scripts.** The `schema` command emits machine-readable
self-description — commands, flags, allowed values, constraints, error codes,
exit codes (note `0` = success *including zero matches*, `3` = empty with
`--fail-empty`), and JSON Schemas for every response. The `recorded` field
in suggest output shows that the app also logs queries as events.

## Stable automation contract

Use `--format json --no-input` for automation. JSON is always canonical English,
including errors on stderr, regardless of `--lang`. Every response includes
`schema_version: 1`. `schema` describes commands, flags, output, and interchange
from the same definitions used by the program. IDs are opaque UUIDs; unique
prefixes are accepted, but scripts should retain full IDs.

The version number identifies the contract major version. Compatible additive
fields are documented in minor application releases; consumers should ignore
unknown fields. Removal or incompatible changes require a new contract major.
`properties` and `property_states` always contain the full property set. States
are `known`, `unknown`, or `not_applicable`. Unknown values are never reported as
known zeroes. `filter_evidence` reports unknown values
that passed a filter. `score_breakdown` is null unless `--explain` is requested.

| Exit | Meaning |
|---|---|
| 0 | Success, including zero matches |
| 1 | Runtime error, unhealthy store, or unresolved sync |
| 2 | Invalid invocation, value, ID, or dependency cycle |
| 3 | No matches when `--fail-empty` is specified |

Errors contain `schema_version` and an `error` object with `code`, `message`,
`value`, and `details`. Invalid parameter values also identify `argument` and
provide `allowed` choices or numeric bounds/date-duration format where applicable.
`schema.responses` describes each JSON command response and errors as JSON Schema;
`schema.commands` exposes the same constraints used by the argument parser.
Completion scripts use these constraints to suggest allowed flag values.
Branch on codes, not message prose. Status commands are idempotent and multiple
IDs are one transaction. `add` and `edit` also set the status directly with
`--status` (same events as the matching status command). `--peek` prevents
suggestion recording. Non-TTY stdin
never triggers interactive prompts; `--no-input` also disables prompts in a TTY.

## Database compatibility and threat model

The working SQLite store uses WAL and **must stay outside a synchronized folder**.
Snapshots are validated, standalone `VACUUM INTO` files. Migrations only move
forward; unsupported schemas are rejected. Preserve a snapshot before upgrading.

`database_uuid` guards against accidental database mixups. It does not authenticate
a peer or defend against somebody who can write arbitrary files into the sync
folder. Treat snapshots as trusted personal data. Inbound files are checked for
schema, identity, SQLite integrity, references, ratings, and dependency cycles.

Writes, initialization, sync, and publication use a bounded OS advisory lock.
Reads do not acquire it. A dead process releases the lock automatically; its
persistent lockfile metadata is overwritten on next acquisition. Sync applies
resolved content in one WAL transaction, preserving readers' existing snapshots.
Published file installation uses fsync and platform replacement. Windows cannot
promise an atomic replacement: consumers validate every inbound snapshot and
reject torn files; the next publish repairs the published file.

## Build and use

Requires Go 1.27.1 or later, including the standard `uuid` package.

```sh
make build
bin/do-something add --title 'Learn basic woodworking' --kind project --type course --total-duration 20h --remaining-duration 20h --interest 1
bin/do-something add --title 'A film' --kind media --type film --total-duration 2h --remaining-duration 2h --quality 1
bin/do-something add --title 'A long-finished series' --kind media --type series --status done
bin/do-something --effort low --session 30m --peek
bin/do-something --mode thinking --engagement focused --effort low --peek
bin/do-something --kind media --effort low --peek
bin/do-something help kind
bin/do-something list --text woodworking --explain
bin/do-something show FULL_ID
bin/do-something start FULL_ID
bin/do-something done FULL_ID
```

`make install` installs to `~/.local/bin` (`PREFIX`/`DESTDIR` supported).
`make dist VERSION=1.0.0` builds Linux, macOS, and Windows, for amd64 and arm64,
with reproducible paths and `dist/SHA256SUMS`. `--help` lists the complete surface.

GitHub releases are automatic. Create and push a semantic-version tag after the
commit has passed CI:

```sh
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

The release workflow reruns the race-enabled test suite, builds the six release
binaries, uploads them with `SHA256SUMS`, and generates release notes. Tags with
a suffix such as `v1.0.0-rc.1` are published as prereleases.

`help [topic]` shows a command page or a concept topic (`kind`, `type`,
`category`, `modes`, `engagement`, `properties`, `scoring`, `filters`). `completion bash|zsh|fish` emits
completion based on the parsed command model.

Bare invocation and `suggest` are aliases; they rank projects by default —
`--kind media` or `both` (projects and media in one list) selects the
rest. `list` includes all kinds and statuses. Prerequisites must be
**done**; dropping one does not unblock its dependents. Use `deps remove` to
explicitly dissolve an abandoned prerequisite.

```sh
do-something deps add ITEM PREREQUISITE
do-something deps list ITEM
do-something deps remove ITEM PREREQUISITE
do-something --include-unready --explain --peek
do-something edit ITEM --clear-cost --clear-deadline --unset-rating interest
```

Item references in `show`, `edit`, `log`, `start`, `done`, `drop`, `reopen`,
`deps`, and the `--depends-on` values of `add`/`edit` accept a full id, a unique id prefix,
or an exact (case-sensitive) title; an ambiguous title is an error
(`INVALID_ID` lists the candidates), and a title that is also an existing
item's id resolves as the id. UUIDs and unique UUID prefixes take priority over
titles. Title matching includes every status. Multi-item status commands resolve
all references before changing anything and list every invalid or ambiguous
reference on failure. Repeated references to the same item are applied once.
For example, `do-something log "A series"` uses that exact title if unambiguous.
Multiple prerequisites are given as separate
arguments; `--depends-on` takes one comma-separated value, where a comma
inside a title is escaped with a backslash (`\,`).

`add` and `edit` also set the lifecycle state directly: `--status
not_started|in_progress|done|dropped`. A status change records the same event
as the matching status command (`started`, `completed`, `dropped`;
`not_started` records none), so history and scoring see it exactly as after a
transition. That is the convenient path for historical items — create them in
the state they are in, without a follow-up `start`/`done`/`drop`. The status
commands remain for moving existing items.

`--session` (`--time` alias) filters the minimum worthwhile session; `--finish-within`
filters remaining duration for both projects and media. Duration values use Go durations (`30m`, `2h`).
`--budget` uses raw, currency-neutral cost. `--mode movement|hands_on|thinking|making`
and `--engagement focused|loose` are not filters but soft re-ranks: each lifts
items that carry the selected value and demotes items that carry other values
only, without excluding anything (items without values on that axis rank in
between). The two axes are independent and combine (`--mode thinking
--engagement focused` = absorb something hard with the mind); engagement is a
preference, not an intensity dial — `--effort` covers demandingness. Unknown
values pass by default;
`--unknown exclude` requires known values. Tags are exact, case-sensitive set
membership; `--tag` and `--not-tag` do not have an unknown state. Text searches
match case-insensitive substrings in title and notes. `--limit 0` returns all
matches; `--random` shuffles matching candidates before limiting.

Interactive add/edit accepts blank to skip/keep and `clear` to unset. The
status prompt accepts a name or `1–4`; status always has a value, so `clear` is
not accepted. Ratings accept 0, 1, or a fraction. Total and remaining
duration offer four configurable estimates, or an explicit duration such as
`30m` or `2h`. The kind prompt shows the kind definitions; `type` and `category` are
free text on every kind, guided by the `vocabulary.types` / `vocabulary.categories`
DB config (nothing ships; new terms can be added there). Titles, types, tags,
and categories remain user data and are never translated.

## Duration and progress

Both kinds use `total_duration`, `remaining_duration`, and `min_session_duration`.
These measure active time, excluding waiting between sessions. CLI flags take
explicit durations (`--total-duration 40h --remaining-duration 30m
--min-session-duration 20m`). Numeric duration values in storage, JSON, and
configuration are **hours**; `schema` exposes `duration_unit: "hours"`.
Text displays seconds below a minute, minutes below an hour, and hours otherwise,
with up to two decimals and no trailing zeroes: `30s`, `25m`, `1.5h`.
Zero is `0m`; large estimates stay in hours with grouped digits (`12,527h`).

Remaining duration drives commitment scoring for both kinds and the
`--finish-within` filter. The shared `scoring.k.remaining_duration` defaults to
20 hours. A smaller remaining commitment scores higher; there is no additional
percentage-based completion bonus. Total duration is context, not a scoring input.

Set each estimate explicitly; neither is inferred from the other. Editing
remaining duration leaves total duration unchanged. Revise total duration when
scope or estimates change. Remaining time alone is enough for scoring.
`--clear-total-duration`, `--clear-remaining-duration`, and
`--clear-min-session-duration` make the respective values unknown.

`list` and `show` report `estimated_progress_percent` as
`100 * (1 - remaining_duration / total_duration)`, with an approximate percentage
in text. Progress is null when either estimate is unknown, total is zero, or
remaining exceeds total. Revised estimates can move progress backward.
Editing remaining time to zero does not mark an item done; use `done` explicitly.

### Ongoing activities

Use `--ongoing` for an activity with no finish line, on either kind:

```sh
do-something add --title "Go for a walk" --kind project --ongoing --min-session-duration 15m
do-something start "Go for a walk"
do-something log "Go for a walk" 30m
do-something log "Go for a walk"          # another 15-minute session
do-something drop "Go for a walk"         # retire the activity
do-something reopen "Go for a walk"       # make it available again
```

Ongoing is distinct from unknown duration. Total and remaining duration are
inapplicable, displayed as `not_applicable` in property states, and cannot be set.
Logs record time spent without a countdown, percentage, or completion prompt.
`log --complete` is an error; `--no-complete` is accepted but unnecessary.
`--session` still works; `--finish-within` always excludes ongoing items, including
when unknown values are allowed. Scoring omits total/remaining duration from groups;
empty groups are omitted and the remaining groups are normalized. Cost and other
applicable properties still contribute normally.

Converting an existing item never silently discards its estimates:

```sh
do-something edit "An existing activity" --ongoing --clear-total-duration --clear-remaining-duration
do-something edit "An existing activity" --ongoing=false --total-duration 2h --remaining-duration 2h
```

Ordinary edits preserve the ongoing flag. Session history records whether the item
was ongoing at the time and remains valid if you later convert it. Query and item
JSON use a boolean `ongoing`; canonical table exports use SQLite's integer 0/1.
An ongoing log has `ongoing: true` and null `remaining_before`/`remaining_after`.
This is not a recurrence scheduler: it does not create a new task every day or
month. Explicit status commands still work; use `drop` to retire an activity.

### Logging activity

```sh
do-something start ITEM
do-something log ITEM 24m            # record time and reduce the remaining estimate
do-something log ITEM                # use the item's min_session_duration
do-something log ITEM --cost 30      # record cost only; no time is inferred
do-something log ITEM 24m --cost 30  # update time and cost atomically
do-something log ITEM 24m --complete # mark done if remaining time reaches zero
do-something log ITEM --no-complete  # skip completion and its prompt
do-something log ITEM 24m --force    # log without changing a non-active status
```

`ITEM` accepts an ID, unique prefix, or exact title. Time and cost are independent;
each may be logged alone or together, and both must be positive. When neither is
given, a known positive minimum session is required. Supplying `--cost` alone never
infers time. A finite item needs a known remaining estimate for each quantity being
logged. An ongoing item may record cost even when `cost_left` is unknown.

Logging normally requires `in_progress`; start the item first. `--force` bypasses
that status check and otherwise leaves status alone. It does not bypass missing
estimates or value validation. Forced logs are allowed for `not_started`, `done`,
and `dropped` items. `--complete` requires logged time, remains invalid for ongoing
items, and cannot complete a `done` or `dropped` item.

Each call records a separate activity event with its timestamp, actual duration
and/or cost, and applicable before/after estimates (numeric hours and cost values
in JSON). Remaining time and cost are clamped to zero on overrun; total duration
stays unchanged. A small tolerance handles floating-point subtraction at zero.
Activities are historical evidence, not operations replayed during sync.

At zero remaining time, interactive use asks `Mark this item done? [y/N]`.
Non-interactive use (including `--no-input`) leaves status unchanged unless
`--complete` is given. `--complete` and `--no-complete` suppress the prompt and
are mutually exclusive; completion occurs only at zero. Declining still records
the session. EOF or an interrupted prompt applies no changes. The activity,
estimate update, and optional completion event are one transaction. Completion
unblocks dependents, just like `done`.

The response includes an event ID, the before/after estimates and statuses, and
estimated progress. Repeated invocations count as separate sessions. To correct
an estimate, use `edit --remaining-duration`; use `reopen` if completion was
accidental. The original activity remains in history.

This unreleased schema is updated in place; pre-release databases and JSON exports
using an older table layout must be recreated or converted before use.

## Configuration and scoring

Neutral built-ins fall back behind database config. There are no personal
weights, `min_session_duration` defaults, or type/category vocabularies in the defaults —
those are database config. `config` lists effective values
with their sources. Cost has **no default half-saturation constant**; it scores
at the unknown prior until calibrated in the unit used by your entries.

```sh
do-something config set scoring.k.cost_left 500
do-something config get scoring.k.cost_left
do-something config unset scoring.k.cost_left
do-something config set vocabulary.types game,film,anime,custom
do-something config set defaults.min_session_duration.custom 0.25
```

Base desirability is a weighted mean of conceptual groups; each group averages
its applicable properties. Unknown properties contribute the 0.5 prior rather
than disappearing. Ratings decay toward the prior; undated ratings do not decay.
Cost curves use `k/(k+value)`. Right-now terms include deadline urgency, momentum,
unblocking, and cooldown, plus mode and engagement matches that are present
only when `--mode` / `--engagement` are given. The default blend is 80% base, 20% right-now. The engine
is pure, bounded, and tested independently of storage. `--explain` includes
contributions, rating ages, priors, and term details. In an interactive text
session it also offers to refresh the top item’s ratings.

Machine settings are separate: `--db`/`--syncdir` > environment
(`DO_SOMETHING_DB`, `DO_SOMETHING_SYNCDIR`) > `local.toml` > platform defaults.
On Linux, locations are `$XDG_DATA_HOME/do-something/list.db` and
`$XDG_CONFIG_HOME/do-something/local.toml` (standard home fallbacks apply).
macOS uses `~/Library/Application Support/do-something`; Windows uses
`%LOCALAPPDATA%` for data and `%APPDATA%` for configuration.
Per-store state sits in `<db path>.state/` so distinct stores never share baselines.

```toml
# local.toml: never synchronized
db = "~/Documents/lists/list.db"
syncdir = "~/Sync/do-something"
color = "auto"
# lang = "sv"
# device_name = "laptop"
```

Language precedence is `--lang` > `DO_SOMETHING_LANG` > local pin >
`LC_ALL`/`LC_MESSAGES`/`LANG` > English. English and Swedish are included;
missing translations fall back to English. Swedish is machine-drafted pending
native-speaker review.

## Import, export, and snapshots

```sh
do-something export json > backup.json
do-something import backup.json --dry-run
do-something import backup.json
do-something import - < backup.json
do-something snapshot safe-backup.db
```

JSON export is full-fidelity: `items`, `ratings`, `item_tags`, `dependencies`,
`config`, and `events`, using normalized rows with SQL column names. Store identity
and metadata are excluded. Import uses newer-`updated_at` item upserts, replaces
the accepted item's ratings/tags/edges, and unions events; conflicting event
payloads are errors. Export/import preserves NULL, rating dates, UUIDs, and events.
Snapshot/sync, rather than JSON, clones database identity.

Import accepts the canonical JSON document emitted by `export json`, regardless
of filename extension. Invalid documents fail as a whole before content is applied.
`--dry-run` reports the proposed merge without changing the store. CSV/TSV, flat JSON
arrays, and spreadsheet profiles are not supported; convert external data to the
canonical JSON structure or use `add`/`edit` for scripted entry.

## Sync workflow

Each device publishes only `snapshots/<device-id>.db`. Configure a folder copier
separately. Mutations publish automatically; changed peers produce an advisory,
never an automatic adoption. On a new machine:

```sh
do-something --syncdir ~/Sync/do-something sync ~/Sync/do-something/snapshots/PEER.db --take-remote
```

That bootstraps the store with the peer's database UUID. For existing stores:

```sh
do-something sync PEER.db --dry-run
do-something sync PEER.db --keep-local
do-something sync PEER.db --take-remote
do-something sync PEER.db --merge --master local
```

Only identical content and event-only divergence resolve automatically. A peer
restored from an old backup still requires an explicit decision. Merge compares
rows against the last **observed** peer snapshot, not a proven common ancestor.
One-sided changes (including deletions) apply; competing changes are reported.
Without a peer baseline, merge is refused. Interactive sync asks for a resolution and allows a local/remote choice for
each conflicting row; `--master local|remote` resolves all competing rows. A merge that would produce
invalid references or a dependency cycle is refused even with a master.

Valid events are unioned in every outcome. Same event UUID with different payloads
is corruption: excluded from the result and reported. Inputs are archived by
content digest, peer baselines are retained, and history keeps the ten most recent
states per peer plus all current baselines. `--dry-run` does not mutate the working
store, publish, archive, or update the baseline. `doctor` reports health, blockers,
configuration, peer snapshots, state disk use, and locale completeness, and runs
history retention cleanup.

## Development

`make test`, `make check`. The CI matrix runs native Linux/macOS/Windows tests,
race tests, vet, staticcheck, govulncheck, and fuzz smoke tests. Cross-compilation
alone is not evidence that native Windows replacement semantics passed.
See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).
