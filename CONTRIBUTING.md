# Contributing

## Before Editing

1. Read [`AGENTS.md`](AGENTS.md) and the affected module's goals and docs.
2. Run `make inventory` and the narrow baseline gate for the module.
3. Identify owned dependencies and reverse dependants in `modules.json`.
4. Preserve unrelated work and generated/corpus provenance.

## Changes

Keep commits focused and conventional. Update every affected changelog with
the behavior and migration impact. Public API changes require compatibility
evidence and documentation. Specification behavior requires a decision record,
fixture coverage, and interoperability evidence.

New direct dependencies and dependency updates must follow the
[dependency governance policy](AGENTS.md#dependencies-and-supply-chain). Package-local
update bots are forbidden; the root policy owns every module and action update.

Specification-backed changes must follow the
[specification governance contract](AGENTS.md#design), update
the affected stable decision entries, and complete the Specification Decisions
section of the pull request template. An unresolved interpretation or stale
source pin is release-blocking; peer behavior cannot silently select policy.

Mutation, race, fuzz, performance, and external-service gates are required only
when the change affects the risk they exercise or at an applicable release
boundary.

Do not add package-local workflows, permanent replacements, machine-specific
paths, bypass flags, broad mutation exclusions, or aggregate quality metrics
that hide a failing package.

## Verification

Classify the change under the Tier A-D assurance model in `AGENTS.md`, then run
the narrowest affected checks. For ordinary source or dependency changes:

```bash
make inventory
golib check --local --module <directory>
```

Use the complete repository contract only for material cross-module risk,
release rehearsal, or an explicit ecosystem milestone:

```bash
make ci
```

Report every unavailable or failing applicable command; do not present an
unrelated unselected gate as a blocker or a pass.

## Adding A Module

Follow [repository structure policy](AGENTS.md#repository-structure). New modules
require an explicit purpose, ownership boundary, dependency review, package
catalog entry, full quality gates, documentation, changelog, license, security
policy, compatibility plan, and release dry-run.
