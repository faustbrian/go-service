# Release boundary

The module is unreleased. Completing the service-platform goal produces a
verified commit tree; it does not select, create, or publish a semantic-version
tag, including `v1.0.0`. Every implementation change remains under
`[Unreleased]` until a maintainer separately authorizes a release version and
moves the entries into a dated section.

The package retains local `make release-patch`, `make release-minor`, and
`make release-major` helpers for a future authorized release. They require a
clean `main` matching `origin/main`, a dated changelog section, a usable OpenPGP
secret key, and a passing package check before creating a local signed tag.
They do not push the tag or publish a GitHub release.

The repository's sole owned CI workflow runs on a published GitHub release,
but it does not create releases, verify a tag signature, build a release
archive, or attest provenance. Those publication capabilities must be designed,
reviewed, and authorized before any future release claim relies on them.
