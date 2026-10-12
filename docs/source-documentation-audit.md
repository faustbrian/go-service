# Source documentation audit

## Ownership and acceptance

This Tier A pass owns source comments and directly contradicted maintained prose
in this repository, based on remote main
`9b3604d082fb497d208071ec18439fb290db8dc7`. It preserves runtime tokens, AST,
literals, directives, API/dependency baselines, fixtures and historical authority.
The coordinator owns policy rollout, publication and exact-main CI.

Consumers are root lifecycle/platform callers, HTTP/health/test-support adapters,
seven root examples, and eight separately pinned compatibility/composition/
measurement modules. Borrowed handlers, callbacks, stores, loggers, observers and
external clients keep their actual ownership boundaries; published reference
bindings must not be confused with current root behavior.

Named risks are cancellation versus joining, startup/shutdown races and error
precedence, callback panic/blocking assumptions, listener/resource transfer,
maintenance disclosure and admission, synchronous probe/observer work, and
reference-module backend trust/transaction limits. This is not a runtime repair,
service/conformance/performance campaign or new evidence framework.

## Reproducible inventory before source edits

`git ls-files '*.go' '**/go.mod'` inventories 160 Go files (75 source, 85 tests),
nine modules and 37 directories. `golib source-docs inventory --json` on the
isolated root reports 18 missing package comments, no initial exported-declaration
structure errors. Zero findings will establish structure, not semantic coverage.

Modules: root; `benchmarks/platform`; `compatibility`; `integration/adoption`;
`integration/reference-durability`; `integration/reference-external`;
`integration/reference-http`; `integration/reference-platform`;
`integration/reference-recipes`. All tracked source, tests, helpers, fuzz,
benchmarks, examples and tags are in scope. No tracked generated/vendor Go or
regeneration declarations were found. Platform signal variants, 21 optional
benchmark mode files, one ignored AST helper and the tagged durability test are
retained; simultaneous benchmark mode tags are not a supported composition.

Missing documentation: command package responsibility and test/benchmark intent.
Investigation before truthful prose: root lifecycle join/error/timeout ownership,
platform resource transfer, HTTP callback/probe behavior, maintenance cache/store
semantics, and nested published-reference/module boundaries. Existing declaration
comments must be checked for overbroad concurrency/privacy/time bounds; rationale
will be sourced from implementation and tests, not inferred historical motives.

## Selected native proof

Read `AGENTS.md`, `CONTRIBUTING.md`, `Makefile`, `.golib.yaml`, module catalogs,
CI and `verification/package.mk`. API mode is `apidiff`, with frozen
`api/baseline.txt`; no full-prose GoDoc snapshot is configured. Baselines and
published module pins are preserved. The docs target runs the tracked AST helper,
root Example tests and builds seven root examples, not all-module/service tests.

After explicit serialized Go 1.27.2 grant: task-owned three caches, all-file
format/token/AST/literal/directive comparison, HTML/source-GoDoc inspection for
all package/variant boundaries (nested renders retain published bindings), native
docs and relevant examples. Current source structure/local links, offline
specification applicability, immutable pinned full/changed/staged secret scans,
frozen/unowned-file comparison and one ordinary complete-final-diff inspection
complete this prose pass. No generators, live services, race/fuzz/mutation,
benchmark comparisons, Kubernetes or clean-consumer campaigns are selected.

## Results and unresolved boundaries

### Source-derived investigation resolved

| Boundary | Contract documented beside source |
| --- | --- |
| Root lifecycle/run | Shallow component registration; cooperative deadlines, partial-start cleanup, one cleanup context, cancellation versus joining, non-reentrant admission and retained terminal errors. |
| Typed process roles | Invocation/plan shallow copies, exclusive selected construction, listener transfer on actual startup, one-shot versus long-running result policy and exit precedence. |
| Maintenance/observability | Plaintext file tokens and explicit generated-token stdout; trusted path/OS blocking, polling staleness, synchronous observer/caller identity disclosure boundaries. |
| HTTP/probes/test support | Middleware construction versus request recovery, body-read overflow, custom context/hijack ownership, globally retained check slots, sequential versus shared deadlines and bounded capture only. |
| Reference modules | Published dependency bindings, borrowed transports/telemetry, reusable grants, destructive disposable DB/stream identities, DB commit versus broker publication, incomplete/repeated external cleanup and process harness limitations. |
| Measurement/examples | Synthetic workload scope, pre-decode byte ownership, optional-tag entrypoints, valid paired sample/prefix rationale, metrics not universal guarantees and placeholder commands. |

All 75 maintained source files were traced; all 85 test files were inventoried
by package, test/helper/tag and named assertions (299 tests, four fuzz targets,
13 benchmarks, nine Examples). Fuzz/benchmark bodies and synchronization,
reference-consumption and recipe helpers were inspected. Existing self-describing
regressions were retained rather than adding comment-only tests. Every exported
member was considered alongside its owning type/implementation; existing accurate
field comments are retained, not rewritten for density.

No generated Go, deprecation or actionable debt markers need producer/issue
changes. Existing suppression directives remain byte-identical. Native DB SQL,
fixture response bodies, certificates, typed/frozen authority and dependency
inputs remain unchanged; no generator is executed for structural acceptance.

### Preserved authority discrepancies and limits

`docs/platform/contracts.md` explicitly freezes Phase 1 normative expectations.
Its blanket “No command prints secrets” conflicts with the later built-in
`down --with-secret` stdout path in maintenanceCommandSpecs. Source/security
comments now explain that intentional credential-output boundary without
rewriting frozen expectations or inventing a new normative decision. Likewise,
its single request-deadline prose describes concurrent probes, not standalone
ModeSequential's per-check deadlines; source comments distinguish them.
No runtime repair or conformance claim follows from this audit. Root named
observation boundaries have byte caps; standalone probe/integration names and
borrowed logger/identity values are not a blanket secrecy/cardinality guarantee.

Non-production referenceexternal.Close marks closed before cleanup and repeated
calls return nil without joining/retrying; it is not a universal barrier for
storage/webhook/hedge work. Benchmark processcore.Run requests graceful shutdown
but does not force-close/join after failure; trusted process termination remains
harness-owned. No stronger lifetime guarantee is claimed. Historical deployment,
performance, threat-model, coverage, mutation and dated review records are
preserved. No new live Postgres/Valkey/Kubernetes, peer benchmark, race/leak/fuzz,
crypto/RFC or production-operational evidence is asserted.

Current all-file structural check has zero findings (initially 18); offline
specification check has zero applicable modules/decisions, not a conformance
verdict. Pinned Gitleaks 8.30.1 with retained immutable Tools policy and ignore
annotations disabled reported zero baseline/current/changed findings; all exact
snapshots/reports were removed. Five relative maintained-Markdown links resolve;
all existing fenced artifacts and every unowned tracked file match base.

### Executed native documentation proof

The explicitly granted bounded Go 1.27.2 darwin/arm64 session used GOWORK=off,
GOMAXPROCS=2, GOFLAGS=-p=2 and three task-owned caches. All 160 tracked Go files
passed native gofmt and exact significant tokens (including literals and implicit
semicolon spelling), comment-free AST and directive preservation against base.
All 752 comment groups parsed and rendered through go/doc/comment. Existing
runtime/API bodies, tests, build tags, directives and dependency/fixture inputs
are unchanged, not re-qualified by a runtime campaign.

Native `go doc -all` succeeded for the five operational root packages and seven
root example packages. Current Execute, Config, HTTP, Maintenance, Shutdown,
probe Config/CheckFunc/Observer, Server New/Run, Probe and WithSlog output was
inspected. Source-only stdlib go/doc renders cover all 37 tracked package
boundaries, including optional tags, ignored helper and test-only fixtures;
they do not certify every platform/tag build or nested published composition.
The native apidiff baseline is frozen and unchanged; no full-prose snapshot
producer is configured.

`make -f verification/package.mk docs` passed the tracked AST documentation
helper, all nine output-bearing root Examples and builds of all seven example
commands. No source-local or published nested composition/service/benchmark
campaign was run. No source/runtime change followed those native checks.

A disposable declaration-display helper initially used an empty FileSet and
misplaced field comments. A short corrective rendering session inspected native
root member output and source documentation as semantic sections instead. Its
supplemental top-level ModeSequential display assertion failed because go/doc
attaches typed enumeration constants to Type.Consts, not package.Consts; the
source enum documentation and native full GoDoc are valid. This is a display
assertion limitation, not a source/test failure or nested API certification.
No executable input was changed to make the display pass. Both exact sessions'
three caches, helpers, renders and native temporary outputs were removed
immediately, including writable cleanup of read-only module-cache directories.
The serialized Go slot was explicitly released.

One ordinary complete-final-diff inspection checked each added/repaired contract
against implementation and focused existing assertions, removing overbroad
retry, timeout, copying and cleanup wording. Source-only API clarification does
not select a module release. Final current/staged content scans
have passed with zero full/staged findings; exact snapshots/reports are removed.
Native local documentation validation passes. Coordinator publication/CI remains
separate from the clean local commit.
