# Release process

The project follows semantic versioning. Before `v1`, incompatible API changes
increment the minor version. Every implementation change belongs under
`[Unreleased]`; a release moves entries into a dated version section.

1. Import an OpenPGP secret key locally, export its base64-encoded public key
   into the repository variable `RELEASE_SIGNING_PUBLIC_KEY`, and set
   `RELEASE_SIGNING_KEY` to the key fingerprint. SSH signing keys are rejected
   because the hosted workflow verifies the imported OpenPGP identity.
2. Ensure local `main` is clean. The release command fetches remote `main`
   without tags and requires the checkout to match the refreshed remote tip.
3. Add the dated changelog section and commit it.
4. Run `make release-patch`, `make release-minor`, or `make release-major`.
5. Inspect the signed tag with `git verify-tag <tag>` and push only that tag.

The tag workflow verifies the signature, semantic tag, changelog, main
ancestry, and complete quality gate. It produces a deterministic `git archive`
with normalized gzip metadata, a SHA-256 checksum, and GitHub provenance
attestation before publishing the release. Missing signing-key configuration,
an unsigned tag, a red gate, or a missing changelog section blocks publication.
