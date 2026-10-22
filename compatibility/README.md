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

## Generated OpenAPI handler

`generated_handler_test.go` is genuine `std-http-server` output from the
Apache-2.0 oapi-codegen v2.8.0 producer, not a handwritten handler labeled as
generated or a new public OpenAPI generator. The finite OpenAPI 3.0.3 input,
configuration, upstream license, exact public module checksums and immutable
producer source are retained in [`testdata/generated-handler`](testdata/generated-handler).
The local driver normalizes only the canonical producer header to identify
that tracked regeneration script, retaining the upstream identity in a separate
comment and leaving every generated declaration unchanged. The generated wrapper
has only standard-library imports and belongs to this non-releasable
compatibility module; no generator dependency reaches Service or its runtime
consumers.

The composition tests mount its actual routing through current published
Service v2.0.0 and `serverhttp`, with separate retained Service v1.1.3 cases.
They send real loopback HTTP requests, check the response,
unknown-path and wrong-method refusal, preserve the Service-owned request
context, and join a canceled request before asserting listener shutdown.
They do not claim schema validation or generated business logic. Both release
identities resolve through public dependencies without a workspace or local
replacement; the historical cases do not substitute for the current v2 cases.

From the repository root, with the repository's `.go-version` toolchain:

```sh
bash scripts/check-generated-handler.sh --check
bash scripts/check-generated-handler.sh --write
```

The default check reproduces exact source bytes without changing the artifact.
Use `--write` only after reviewing the specification, configuration and producer
record. The script uses authenticated public proxy/SumDB resolution, checks
the producer's exact source identity and checksums before execution, and owns
and removes its build/module/temp caches. Generator advisories for malicious
specification injection remain relevant: this fixture accepts only its reviewed,
byte-pinned local inputs, without references, extensions or user templates;
the generated imports and initialization boundary are checked before compilation.
It is not an arbitrary-input generator sandbox.
