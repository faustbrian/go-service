# Contributing

## Before Editing

1. Read [`AGENTS.md`](AGENTS.md) and the affected module's current docs.
2. Run `make inventory` and the narrow baseline gate for the module.
3. Identify affected owned dependencies and reverse dependants in
   `modules.json`.
4. Preserve unrelated work and release-bound source attribution.

## Changes

Keep commits focused and conventional. Update affected changelogs for
user-visible behavior or migration impact. Public API changes require the
applicable compatibility evidence and documentation.

New direct dependencies and dependency updates must follow the
[dependency governance policy](AGENTS.md#dependencies-and-supply-chain). Package-local
update bots are forbidden; the root policy owns every module and action update.

Specification-backed changes must follow the
[specification governance contract](AGENTS.md#design). Update a decision record
and run conformance evidence when the change affects an interpretation or
claimed specification behavior. Peer behavior cannot silently select policy.

Mutation, race, fuzz, performance, and external-service gates are required only
when the change affects the risk they exercise or at an applicable release
boundary.

Do not add package-local workflows, permanent replacements, machine-specific
paths, bypass flags, broad mutation exclusions, or aggregate quality metrics
that hide a failing package.

## Verification

Classify the change under the Tier A-D assurance model in `AGENTS.md`. Use
Go 1.27.3; public module minima remain Go 1.27.0. An isolated checkout needs
no sibling repositories or coordination goal files. Begin with deterministic
root tests and one active target when lifecycle configuration changes, using
a disposable cache per bounded session:

```bash
(
  set -eu
  GOCACHE="$(mktemp -d)"
  export GOCACHE
  trap 'rm -rf "${GOCACHE}"' EXIT
  GOWORK=off go test ./...
  GOWORK=off go test . -run '^$' -fuzz '^FuzzConfig$' -fuzztime=2s -timeout=2m
)
```

Deterministic tests execute fuzz seeds/corpus, not active fuzzing, and omit
intentional nested modules. Install the public binary archive at the version
declared in `.golib.yaml` for your platform from
[go-library-tools releases](https://github.com/faustbrian/go-library-tools/releases),
checking `checksums.txt` against `.golib.yaml` and the archive against that
checksum set. Put the extracted `golib` binary on `PATH`, or select it through
`GOLIB`; do not build Tools from source. CI uses this public release while
checking out its immutable reusable workflow source.

Run adjacent commands in one disposable-cache session as shown above:

```bash
make inventory
make check
make ci
make specification-check
golib check --local --module compatibility
golib mutation --module . --package .
```

`make check` and `make ci` select the bounded contract. The root lifecycle
race runs once with a two-minute timeout: concurrent callers run one cleanup
while canceled waiters can leave; shutdown cancels and joins startup before
returning stopped; later components cannot start, and a transferred component
is rolled back. These component assertions do not certify every composed
reference service or live durability semantics. Base-free checks run seeds/
corpus; changed-source CI selects causal active fuzz smoke. Optional mutation
selects one production package, one worker and a twenty-minute deadline.
Offline specification checking is ordinary.

All nine modules retain separate public/dependency boundaries. Native scoped
checks run inside the selected module with `GOWORK=off`. All-module checks
retain the `integration/reference-durability` PostgreSQL/Valkey services and
`integration` tag; they are not Docker- or database-free. Select its affected
live integration boundary when durability behavior changes.

CI accepts false-by-default `platform_containers`, `constrained_load`,
`durability_recovery`, `durability_version_matrix`, and `durability_version_skew`
manual-dispatch inputs. The skew selection exercises independent released Queue
consumers; `check-version-skew.sh --build-only` checks their public build inputs
without starting the hosted-only process/backend campaign.
The first two use separate native Linux amd64 and arm64 runners without QEMU;
the recovery and supported PostgreSQL matrix campaigns use isolated Linux amd64
runners. Selected jobs must pass Required; unselected jobs remain skipped.
Scenario logs and summaries identify the checked-out source SHA, runner and
actual outcome. These are non-production qualification, not deployment or
universal platform-readiness evidence.

The workflow also accepts the optional `durability_composition`
manual-dispatch input. It runs the native `check-durability.sh` composition
against digest-pinned PostgreSQL 18.4 and Valkey 9.1.0 on an isolated hosted
runner, with public dependencies and disposable caches and service resources.
The selected result must pass Required; an unselected job is skipped, not
passing evidence. This proves transaction/replay/rollback and queue
handoff/drain/acknowledgement, not the separate destructive process-recovery
campaign or the complete supported-version matrix. Run those only for a named
recovery or version-compatibility requirement.

Use the complete repository contract for material cross-module risk, release
rehearsal or an explicit ecosystem milestone:

```bash
make check-full
make specification-check-online
make -f verification/package.mk docs
make -f verification/package.mk candidate-consumer
make -f verification/package.mk kubernetes
golib api check
golib release dry-run --module .
```

Full checks preserve all three fuzz targets at 5s/20m, the 100ms/20m benchmark,
API documentation/examples and candidate-composition interoperability operations,
mutation imports and API baseline. The disposable Kubernetes gate remains
explicit, not an ordinary prerequisite. Scheduled CI uses the shared bounded
route, not an automatic full composition campaign. Report unavailable or
failing applicable commands; bounded checks alone do not establish release
qualification, and an unrelated unselected gate is neither a blocker nor a pass.
After publication, `make -f verification/package.mk clean-consumer` must resolve
the released version through the public proxy without replacements. Candidate
composition before tagging does not satisfy that public-consumer boundary.

## Adding A Module

Follow [repository structure policy](AGENTS.md#repository-structure). New
modules require an explicit purpose, ownership boundary, dependency review,
catalog entries, and assurance proportional to their public contract and
risks. Run release rehearsal only when preparing that module for publication.

## Source Documentation

Install the checksum-pinned public verifier declared in `.golib.yaml`.
`make source-docs-check` validates objective Go comment, marker, link and
generated-producer structure; `make check`, `make check-full` and `make ci`
require it before their existing checks. Shared CI runs the same strict
contract and includes its result in Required. This is structural enforcement,
not a substitute for source-truth review or behavioral verification.
