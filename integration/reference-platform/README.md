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
