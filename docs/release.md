# Release boundary

The module starts its stable release history at `v1.0.0`. Subsequent releases
move completed entries from `[Unreleased]` into a dated semantic-version
section.

Release the root module from a clean `main` matching `origin/main`, with the
intended version in `modules.json` and a dated changelog section. Require green
relevant exact-source CI and the existing `release_dry_run` rehearsal before
creating an annotated SSH-signed semantic-version tag with the configured
maintainer key. Verify the tag against the trusted maintainer public key, push
it normally, and publish a stable GitHub release from that tag.

The existing publication pattern is source-only: GitHub provides its source
archives, and no separately built release assets or artifact provenance are
claimed. Verify the public Go proxy and SumDB source identity and execute an
ordinary clean public consumer without replacements before claiming the
release boundary complete. Publication is not application deployment.

The sole owned CI workflow runs on pull requests, pushes to `main`, a schedule,
and manual dispatch; it does not run on GitHub release publication or create
tags, releases, archives, or provenance. The Makefile delegates verification to
`golib`; it does not provide release-tag helpers.

The eight nested composition modules are non-releasable fixtures and are not
separately tagged. Necessary future major releases remain on `main`, retaining
Go's required major module/import suffix without version-specific source
directories or branches.
