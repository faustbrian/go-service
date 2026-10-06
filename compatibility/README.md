# Service compatibility harness

This non-production module verifies that the public Service API composes with
the supported authentication, authorization, configuration, logging, queue,
scheduler, and telemetry libraries through their released module identities.

It is an executable compatibility fixture, not an application dependency or a
separate release unit.

Authentication uses its `/v2` module and canonical `adapters/http` package.
The HTTP contracts preserve explicit optional-anonymous composition and verify
authenticated bearer identity reaches the structural authorization mapper.
Missing or rejected credentials cannot reach the mapper or application.
