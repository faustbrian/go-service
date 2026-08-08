# Security guide

## Trust boundaries

Component names, health check names, correlation identifiers, log attributes,
and public errors cross diagnostic boundaries. Do not place secrets or
tenant-controlled high-cardinality values in them. Health details expose only
configured names and binary statuses; dependency errors and panic values are
not serialized.

Inbound correlation metadata is untrusted by default. Enabling trust is
appropriate only behind an authenticated proxy that removes client-supplied
values. Every accepted request receives a new request ID; a trusted prior
request ID becomes causation. Authentication and authorization remain
application middleware and must not be inferred from correlation metadata.

Maintenance bypass tokens are application-owned credentials. They are
validated as URL-safe values, never emitted by status, errors, logs, or runtime
events, and are represented in the browser cookie only by a domain-separated
digest. Distribute them through a secret channel and rotate them by publishing
a new maintenance state. Shared-store adapters must protect the state at rest
and in transit.

## HTTP

Defaults bound header read, full read, write, idle, body, header, and shutdown
resources. Explicit zero request timeouts disable standard-library limits and
therefore require a documented deployment reason. TLS versions, certificates,
trusted proxies, CORS, authentication, authorization, and route policy remain
caller-owned.

Recovery removes prepared headers and returns a generic 500 only before commit.
HTTP cannot retract bytes already written; committed-response panics are
contained but the partial response remains visible.

## Process and dependencies

`GO-SAFETY-1` rejects production `unsafe`, cgo, and `go:linkname`. The cohesive
root depends only on owned `cli` and `correlation` modules; optional adapters
remain isolated. The sole owned CI workflow runs the cataloged module security
gates and CodeQL. Dependency updates are configured through Dependabot. This
repository does not currently own a hosted service-tag publication or archive
provenance workflow.

Report vulnerabilities through the private process in `SECURITY.md`.
