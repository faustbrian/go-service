# Platform reference service

This non-production module verifies the public `service` process model in
disposable Linux containers. Its platform harness builds and runs both
`linux/amd64` and `linux/arm64` images with `CGO_ENABLED=0`, a non-root user, a
read-only root filesystem, a bounded writable temporary filesystem, dropped
capabilities, process and descriptor limits, health probes, DNS, private TLS
trust, and graceful `SIGTERM` handling.

`check-load.sh` adds a bounded native-architecture campaign under the same
64 MiB, 0.25 CPU, process, descriptor, read-only filesystem, and non-root
constraints. It drives an equivalent fixed response, samples process resources,
and fails explicit error, latency, throughput, heap, goroutine, and descriptor
budgets. This short campaign is not a soak-test substitute.

The harness is local container evidence. It does not claim live ECS, task IAM,
Graviton hardware, production networking, or deployed-service readiness.

## Hosted qualification

Dispatch CI with `platform_containers` or `constrained_load` to run the selected
campaign on separate native Linux amd64 and arm64 runners. Both inputs default
to false; ordinary PR, main and scheduled checks do not run these campaigns.
Each selected result is mandatory for Required, and native job logs and summaries
retain the checked-out source SHA, runner architecture and scenario outcome.

`./integration/reference-platform/check-platform.sh` retains the two-architecture
matrix. Supplying `amd64` or `arm64` selects one architecture and refuses a
non-matching host before allocating resources; hosted CI uses that native route.
The scripts require Docker with Buildx, Go 1.27.3, curl and jq. They use public
checksum-authenticated dependencies and digest-pinned Go and BuildKit inputs,
with private temporary caches and task-owned containers, images and builders.
The fixture is built before its ten-second readiness budget begins. These
non-production checks do not establish ECS deployment, task IAM or production
readiness; successful load results apply only to the existing bounded workload
and resource budgets.
