# Security guide

## Trust boundaries

Component names, health check names, request IDs, log attributes, and public
errors cross diagnostic boundaries. Do not place secrets or tenant-controlled
high-cardinality values in them. Health details expose only configured names and
binary statuses; dependency errors and panic values are not serialized.

Inbound request IDs are untrusted by default. Enabling trust is appropriate
only behind a proxy that removes client-supplied IDs and creates a bounded HTTP
token. Authentication and authorization remain application middleware and must
not be inferred from correlation metadata.

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

`GO-SAFETY-1` rejects production `unsafe`, cgo, and `go:linkname`. The module has
no third-party production dependency. CI runs `govulncheck`, dependency review,
and pinned action updates. Release tags require an imported signing public key,
and archives receive signed provenance attestations.

Report vulnerabilities through the private process in `SECURITY.md`.
