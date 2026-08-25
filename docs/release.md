# Release boundary

The module starts its stable release history at `v1.0.0`. Subsequent releases
move completed entries from `[Unreleased]` into a dated semantic-version
section.

The package retains local `make release-patch`, `make release-minor`, and
`make release-major` helpers. They require a
clean `main` matching `origin/main`, a dated changelog section, a usable OpenPGP
secret key, and a passing package check before creating a local signed tag.
They do not push the tag or publish a GitHub release.

The repository's sole owned CI workflow runs on a published GitHub release,
but it does not create releases, verify a tag signature, build a release
archive, or attest provenance. Those publication capabilities must be designed,
reviewed, and authorized before any future release claim relies on them.
