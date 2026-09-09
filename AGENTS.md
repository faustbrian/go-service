# Engineering Policy

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

## Scope And Authority

- This file is the canonical policy for the complete repository.
- Package policies MAY add stricter domain rules but MUST NOT weaken this file.
- `CLAUDE.md` and tool-specific files MUST point here rather than duplicate it.
- Historical `.ai/GOAL*.md` files are requirements and evidence, not proof of
  completion. Current executable evidence is REQUIRED.

## Repository Structure

- The public root module MUST live at the repository root.
- Intentional optional or test modules MAY live in explicit nested directories.
- Commands MUST live under `cmd/`; private shared code MUST live under
  `internal/`; root automation MUST live under `scripts/`.
- Public module paths MUST match their repository-relative directories beneath
  the module path declared by the root `go.mod`.
- Every module MUST be declared in `modules.json`, and every package MUST be
  declared in `packages.json`.
- Independently releasable modules MUST retain independent `go.mod` files and
  directory-prefixed semantic-version tags.
- Cross-module dependencies MUST remain acyclic and MUST use public contracts.
- Permanent `replace` directives, sibling repositories, and absolute developer
  paths are forbidden in releasable modules.

## Design

- Prefer standard-library interfaces and explicit composition over hidden
  registration, global state, reflection-driven wiring, or service locators.
- Public APIs MUST make ownership, cancellation, retries, timeouts, resource
  limits, error semantics, and concurrency behavior observable.
- Interfaces SHOULD be defined by consumers and MUST remain narrowly scoped.
- Optional integrations SHOULD be adapters or nested modules, not mandatory
  dependencies of a core package.
- Breaking protocol or specification ambiguities MUST be documented as explicit
  decisions and covered by tests.

## Safety And Concurrency

- Shared mutable state MUST have one documented synchronization owner.
- Goroutines MUST have explicit lifetime, cancellation, and shutdown.
  Fire-and-forget goroutines are forbidden. Add leak or stress tests when a
  change materially affects those risks.
- Channels MUST have documented ownership and closure rules.
- Locks MUST NOT be held across caller callbacks, network IO, blocking channel
  operations, or unbounded work.
- Every external operation MUST accept or derive a bounded `context.Context`.
- Response bodies, files, rows, transactions, timers, tickers, connections,
  and temporary resources MUST be closed on every path.
- Integer conversions, sizes, offsets, recursion, decompression, and allocation
  from untrusted input MUST be bounded before allocation or conversion.
- Secrets and credentials MUST NOT appear in errors, logs, traces, snapshots,
  fixtures, mutation reports, or generated artifacts.

## Proportional Assurance

Classify each change before selecting verification:

- **Tier A** covers documentation, metadata, registration, generated
  documentation, and dependency-path changes without runtime behavior. Verify
  the affected structure, links, examples, module tidiness, and final diff.
- **Tier B** covers internal behavior without a public contract change. Run
  focused behavior tests, affected package or module tests, applicable format
  and static checks, and one complete review.
- **Tier C** covers public APIs, lifecycle, security, persistence, and
  concurrency. Require an observable regression or characterization test,
  focused behavior, API compatibility where applicable, directly affected
  package and integration tests, direct owned reverse consumers, and one
  independent complete-diff review.
- **Tier D** covers public releases and ecosystem milestones. Bind immutable
  release or milestone inputs once and run only the relevant compatibility,
  composition, consumer, and aggregate checks.

Behavioral tests MUST assert outcomes, invariants, errors, cleanup, and state
transitions. Line execution alone is not behavioral proof. Race, fuzz,
mutation, leak, performance, conformance, external-service, clean-consumer,
release-rehearsal, and aggregate fleet checks MUST run only when they exercise
a material risk or the applicable Tier D boundary. They MUST NOT block an
unrelated change merely because the check exists.

Parsers and hostile boundaries SHOULD use fuzzing when malformed or adversarial
input is a material risk. Concurrent behavior changes MUST use race and
targeted stress or leak checks when those risks are affected. Benchmarks MUST
compare equivalent behavior and publish their environment and statistical
method when performance is a stated contract.

## Verification Commands

- `make inventory` validates repository and package manifests when those
  manifests or module boundaries change.
- `golib check --local --module <directory>` runs the bounded module contract
  for ordinary source and dependency changes.
- `make check` and `make ci` run the complete enabled repository contract and
  are reserved for material cross-module risk, release rehearsal, or an
  explicit milestone requirement.
- Missing tools or services fail only an applicable required gate. Optional or
  unrelated gates MUST NOT be promoted into completion blockers.
- NilAway is advisory; its findings SHOULD remain visible against the current
  baseline.

## Evidence Validity And Reuse

- Evidence MUST support only the behavior, module, consumer, or release
  boundary it actually exercised.
- Fresh evidence is REQUIRED after a change to an input that can affect the
  claimed result. Unchanged immutable inputs SHOULD reuse their existing
  evidence.
- A history-only change, metadata-only change, or unrelated-file change MUST
  NOT force an expensive gate to rerun when its behavior-affecting inputs are
  unchanged.
- Commit identifiers and tool versions MAY be recorded at delivery boundaries,
  but routine progress notes, reviews, and local test output MUST NOT require
  recursive hashes or provenance records.
- Long-running applicable gates SHOULD checkpoint independently valid units so
  interruption does not discard completed work.
- Temporary execution output and caches MUST be removed after the relevant
  result is captured.

## CI And Workflows

- `.github/workflows/ci.yml` is the only owned GitHub Actions workflow.
- Package-local workflows MUST NOT be added.
- Actions and external tools MUST be pinned to immutable versions.
- Every module selected by the applicable contract MUST have an attributable
  result. Persist an evidence artifact only when the gate produces material
  reusable or release-bound evidence.
- The stable required job MUST fail for failed, cancelled, skipped, or missing
  module results.
- Required checks MUST NOT use `continue-on-error`, `|| true`, permissive
  thresholds, or warning substitutions.

## Dependencies And Supply Chain

- Dependencies MUST be necessary, maintained, license-compatible, and pinned to
  reviewed supported versions.
- Standard-library functionality MUST NOT be wrapped merely to create an owned
  abstraction; wrappers require a stable policy or portability boundary.
- Generated code and vendored corpora MUST retain enough source and license
  information to reproduce or audit them. Immutable artifact digests belong at
  the applicable release or external trust boundary, not in routine edits.
- Vulnerability, secret, license, SBOM, provenance, and clean-consumer checks
  are Tier D release gates when applicable to the released artifact.

## Documentation

- Public identifiers MUST have useful Go documentation describing semantics,
  invariants, ownership, errors, concurrency, and caveats where relevant.
- Comments MUST explain why a constraint or non-obvious implementation exists;
  they MUST NOT narrate obvious syntax.
- Public modules MUST document the adoption path, API contract, and material
  operational or security constraints appropriate to their audience and
  maturity. Documentation sections without relevant content are OPTIONAL.
- Applicable executable documentation and examples MUST compile in the
  selected module's bounded CI contract.

## Changelogs

- Every user-visible change MUST update the affected module `CHANGELOG.md` in
  the same commit.
- Entries MUST describe behavior and migration impact, not internal activity.
- Changes to multiple modules MUST update every affected changelog.
- Unreleased entries MUST NOT be silently rewritten or removed.
- Generated, dependency, security, compatibility, and deprecation changes
  require entries only when they alter supported behavior, adoption,
  migration, risk, or release output.

## Completion

- Run the narrowest affected gates during development and only the applicable
  Tier D gates before a release or ecosystem milestone.
- Re-run a gate after a later change only when that change can affect the
  gate's claim.
- Report exact commands and results. A skipped, blocked, stale, or warning-only
  applicable gate is not a pass; an unrelated unselected gate is not a blocker.
