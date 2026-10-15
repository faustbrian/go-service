# Durability reference service

This maintained non-production module exercises Golib's PostgreSQL and Valkey
durability stack through public APIs. It is assurance infrastructure, not a
deployable product or an application dependency.

Normal staging and fresh-process recovery select the published Outbox v2.0.0
envelope, PostgreSQL writer and relay together with its queue adapter v2.0.0.
The queue supplier remains at the harness's existing v1.1.2 selection. These
nominal contracts compose with the existing Idempotency v2 adapter without
changing transaction or lifecycle ownership; no Service root release is needed.

The selected hosted durability check also covers two directed public edges.
Scheduler v2.0.2 replicas share its own PostgreSQL occurrence lease and dispatch
one canonical occurrence through Queue's existing Valkey Streams supplier.
Separately, a committed Outbox intent reaches Webhook v3.0.0 through the durable
queue: a real loopback HTTP 503 receives one attempt and no acknowledgement;
consumer replacement reclaims identical bytes, HTTP 204 succeeds, and explicit
acknowledgement leaves no pending or lagging work. This is at-least-once delivery,
not atomic HTTP/SQL commit or an exactly-once webhook guarantee.

These additions retain the existing direct Queue, PostgreSQL, Migrations,
Idempotency and Outbox versions. Scheduler necessarily raises shared Service,
Correlation, Identifier and telemetry/network versions through Go's normal
minimal version selection. Earlier runtime evidence for the previous dependency
cohort does not qualify this changed build list; the hosted check executes all
three selected scenarios with the race detector against the existing
digest-pinned fixtures. External
Lease v2 is not part of Scheduler's own lease port, and no unrelated library
permutation or destructive recovery campaign is selected.

PostgreSQL v2 configuration uses an explicit fixture-owned pgx resolver,
including any native environment and TLS-file acquisition. Startup ping remains
explicitly enabled so the scenario still fails before work on unavailable
database dependencies; pool shutdown remains owned by the scenario.

The executable scenario is intentionally bounded to PostgreSQL migrations,
rollback isolation and atomic commit of business, idempotency-completion, and
outbox state, Valkey Streams publication, consumer restart with
unacknowledged-task reclamation, application handling, and command replay. The
recovery consumer is owned by the public queue/service lifecycle: drain first
withdraws intake while an admitted handler remains active, acknowledgement is
observably absent until application processing succeeds, and shutdown releases
the concrete worker exactly once after the admitted work drains. Kafka,
OpenSearch, provider failover, managed-service behavior, load, soak, and
production readiness remain outside this module's evidence boundary.

`check-recovery.sh` adds a destructive but task-owned local recovery campaign.
It terminates a prepared application process with `SIGKILL`, kills and replaces
both dependency containers while retaining their dedicated durable volumes,
proves PostgreSQL and Valkey outages fail closed, and then verifies exact
idempotency replay, business and outbox state, queue reclamation, and
acknowledgement from a fresh process. A second Valkey crash and replacement
proves the consumer group retains zero pending or lagging work. This does not
claim managed failover, network partition, ambiguous PostgreSQL commit,
backup/restore, or cross-region recovery.

`check-version-matrix.sh` runs the same public durability composition against
digest-pinned PostgreSQL 14 through 18 and Valkey 9.1.0. Every backend pair,
network, image pull, Go cache, and module cache is task-owned and removed after
the campaign. The matrix proves supported-version composition; it does not
claim managed-service behavior, upgrade-in-place, failover, or production
capacity.

## Released Queue version skew

`check-version-skew.sh --build-only` builds the same maintained public consumer
source in two independent temporary modules, without replacements or a parent
workspace. It authenticates Queue v1.1.0 source
`29797a97f747555d0cd00aa38dfe11f22e08f456` and v1.1.2 source
`2173052b630557f345d02ec6961594218d47a316`, then checks each binary's exact Queue
dependency identity so minimal-version selection cannot silently raise the old
consumer. This mode does not start Docker or certify runtime compatibility.

Select CI's false-by-default `durability_version_skew` input for the actual
hosted campaign. It uses the existing digest-pinned Valkey 9.1.0 image with
AOF/always-fsync and task-owned containers, volume and private Go caches. The
old consumer publishes and claims one common-fields task, is killed and joined,
and the new consumer reclaims and acknowledges it. The reverse direction
publishes with the new version, gracefully stops/joins that owner, and reclaims
with the old version. Two independently built consumers also compete over eight
tasks, with both held after a first claim before acknowledgement is released;
reclaim is disabled while those owners remain live. Exact task identities,
common payload/execution fields, bounded claim counts and acknowledgements are
checked. Both releases delete fully settled entries; retaining those deleted
bytes is not the persistence oracle. One additional unconsumed public task per
cohort supplies persistent data. After backend replacement and before opening
any worker, its exact entry bytes and the pre-existing group metadata/cursor
must match the pre-kill snapshot, with zero pending work and one lagging task.
The old client consumes exactly that sentinel, then both versions verify zero
remaining depth, pending work and lag. This distinguishes persisted ack state
from an empty or silently recreated group without claiming acked payload
retention.

This is stopped-owner, stable-major common-fields compatibility, not a schema
migration, arbitrary version permutation, managed failover or production claim.
Queue XACK settlement has no consumer fencing token: a stale live worker's
ability to acknowledge after transfer is not excluded. Owners must be stopped
or drained before transfer; this campaign does not claim live stale-owner
fencing or exactly-once external effects. Required distinguishes an unselected
skipped campaign from a successful selected result; job logs/summary retain the
source and actual scenario outcome.
