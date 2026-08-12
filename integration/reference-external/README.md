# External dependency reference service

This non-production module proves that Golib's public outbound-integration APIs
compose without a private framework layer. It covers bounded HTTP calls,
standalone resilience controls, signed webhook delivery, filesystem storage,
and authenticated secret envelopes.

The module uses injected transports, storage, endpoint policy, and key
providers. Its tests use loopback HTTP servers and in-memory adapters. Live
cloud-provider, container, chaos, soak, deployment, and production evidence
remain separate operational-assurance scenarios.
