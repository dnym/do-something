# Contributing

Use Go 1.27.1+, pure-Go runtime dependencies, and `gofmt`. Run `make test` and
`make check` before submitting a change. Build other targets with `make dist`.
The project retains the license in LICENSE.

Keep personal vocabularies, calibration, database files, spreadsheet exports,
local development instructions, and local import profiles out of commits.
Tests should use generated temporary stores and generic fixtures.

Scoring takes data and configuration as arguments and performs no I/O. Changes
to scoring require table tests. Storage access belongs in internal/store;
mutations and publication must share the per-store application lock. Validate
all inbound snapshots and use the store replacement primitive for files.

All human-facing text belongs in internal/i18n/locales; JSON remains canonical
English. English and Swedish catalog keys should match. Native-speaker review
of the Swedish catalog is a release checkpoint.

Changes to persisted data require a forward migration and preservation tests.
Update the generated schema contract and README for CLI changes. Add sync tests
for any new decision branch; never infer ancestry from digest equality.
