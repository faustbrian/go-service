# Platform reference service

This non-production module verifies the public `service` process model in
disposable Linux containers. Its platform harness builds and runs both
`linux/amd64` and `linux/arm64` images with `CGO_ENABLED=0`, a non-root user, a
read-only root filesystem, a bounded writable temporary filesystem, dropped
capabilities, process and descriptor limits, health probes, DNS, private TLS
trust, and graceful `SIGTERM` handling.

The harness is local container evidence. It does not claim live ECS, task IAM,
Graviton hardware, production networking, or deployed-service readiness.
