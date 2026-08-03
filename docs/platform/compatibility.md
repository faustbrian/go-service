# Platform compatibility and migration

This document freezes the Phase 1 consumer, API, and migration constraints.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

## Publication state

No local or remote `pkg/service/*` tag exists at the Phase 1 revision. The
module manifest describes the module as unreleased. README, compatibility,
evidence, hardening, and changelog text MUST NOT claim an existing stable
release.

This goal MUST end at a verified commit tree and MUST NOT select, reserve,
create, or publish a module tag. Any future publication version requires a
separate maintainer decision and authorization.

## Current package surface

| Import | Current role | Platform disposition |
| --- | --- | --- |
| root `service` | documentation-only package named `goservice` | replaced by root lifecycle and cohesive API |
| `service/service` | lifecycle, signals, supervision | moved to root; nested package removed |
| `service/serverhttp` | HTTP runtime and legacy request IDs | retained; correlation ownership migrated |
| `service/healthhttp` | health handlers importing nested lifecycle state | retained; root dependency removed |
| `service/integration` | generic lifecycle hooks | retained only where not superseded by typed owning adapters |
| `service/servicetest` | lifecycle/probe test helpers | retained and expanded |

The move is intentionally breaking before the first publication. A permanent
`service/service` facade would preserve the wrong primary entry point and MUST
NOT remain in the initial published tree.

## Repository consumers

Every following consumer MUST migrate in the same coherent change as root
consolidation:

- all `pkg/service` examples;
- module and isolated compatibility tests;
- `healthhttp`;
- `integration`;
- `servicetest`;
- API and package manifests;
- `pkg/lease/leaseservice`;
- `pkg/http-middleware/integration/siblings`;
- documentation and code examples; and
- Track bootstrap fixtures that currently import `service/service`.

Repository-wide search and manifest inventory MUST prove there is no remaining
production, test, example, baseline, or documentation reference to the nested
package.

## Exported API migration

The current lifecycle identifiers move without semantic weakening:

- `Component`;
- `Config`;
- `Service`;
- `State` and state constants;
- `New`;
- `Run`, `RunWithSignals`, `Wait`, and `WaitWithSignals`;
- lifecycle error types and sentinels; and
- task supervision and shutdown methods.

The move MAY refine names or signatures only where the Phase 1 platform
contract requires it. Compatibility fixtures MUST cover both direct low-level
root usage and the cohesive `Definition` path.

`serverhttp.RequestIDs`, `RequestIDConfig`, `RequestIDGenerator`, and
`RequestID` have ambiguous pre-publication semantics. They MUST be removed from
the stable API and replaced by correlation-owned types and adapters. An alias MUST
NOT continue treating `X-Request-ID` as a workflow correlation identifier.

Health paths used by old examples (`/live`, `/startup`, `/ready`) are not
stable aliases. Canonical paths are `/livez`, `/startupz`, and `/readyz`.
Legacy aliases, if needed by a consumer spike, require an explicit
compatibility option and are absent by default.

## API baseline

The existing `api/baseline.txt` records the pre-platform surface. Root
consolidation MUST regenerate it only after:

1. all repository consumers compile against root `service`;
2. behavioral regressions pass;
3. architecture tests prove the new direction; and
4. the complete final diff has no unresolved review finding.

The final baseline MUST include the cohesive API, low-level root lifecycle,
retained subpackages, and `servicetest`. It MUST exclude `goservice`, the
nested lifecycle import path, and ambiguous request-ID APIs.

## Go and dependency compatibility

The platform uses the repository Go version until release policy selects the
published minimum. Root runtime dependencies are restricted to the standard
library plus stable `cli` and `correlation` modules.

For this goal, `service` MUST pin reachable immutable sibling revisions and
`GOWORK=off` clean-consumer verification MUST pass without a workspace or
`replace` directive. Selecting stable dependency versions belongs to any
future separately authorized publication plan and is not a completion
requirement here.

Owning-module adapters MAY use the workspace before publication, but their
evidence is labeled pre-publication.

## Consumer migration sequence

1. Move lifecycle implementation to root and update internal subpackages.
2. Add cohesive construction without removing direct low-level composition.
3. Replace legacy request IDs with correlation-owned HTTP behavior.
4. Update examples, repository consumers, manifests, and the API baseline.
5. Add typed owning-module adapters in dependency order.
6. Run bounded Track, Postal, and Location migration spikes.
7. Remove pre-publication compatibility bridges and false stable-version
   claims.
8. Run clean-consumer and complete affected verification gates.

The sequence is one pre-publication migration plan, not a promise to publish
intermediate incompatible versions.

## Compatibility fixtures

Compile and runtime fixtures MUST cover:

- root lifecycle-only use;
- cohesive `Main` and deterministic `Execute`;
- caller-owned `http.Handler`, listener, logger, and telemetry;
- `serverhttp` and `healthhttp` direct use;
- typed configuration loading;
- every mandatory owning-module adapter;
- composition-only logging, HTTP client/middleware, router, authentication,
  authorization, JSON-RPC, JSON:API, and generated OpenAPI handlers;
- all four standard roles;
- custom one-shot and long-running commands; and
- Track, Postal, and Location spike definitions.

Fixtures prove only the contracts they exercise. Workspace fixtures MUST NOT
be presented as published-resolution evidence.

## SemVer decision

Every change described here occurs before the first stable release. After a
future initial publication, exported API and documented wire incompatibilities
require the repository's SemVer process. Before publication, correctness and
the fixed root-package decision take precedence over preserving defective
import paths.
