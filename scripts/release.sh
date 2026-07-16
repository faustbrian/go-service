#!/usr/bin/env bash
set -euo pipefail

part="${1:-}"
case "$part" in
  patch|minor|major) ;;
  *)
    echo "usage: scripts/release.sh <patch|minor|major>" >&2
    exit 2
    ;;
esac

root="$(git rev-parse --show-toplevel)"
cd "$root"

if [[ "$(git branch --show-current)" != "main" ]]; then
  echo "releases must be created from main" >&2
  exit 1
fi
if ! git diff --quiet || ! git diff --cached --quiet ||
  [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
  echo "release requires a clean working tree" >&2
  exit 1
fi
git fetch --quiet --no-tags origin \
  +refs/heads/main:refs/remotes/origin/main
if ! git rev-parse --quiet --verify origin/main >/dev/null ||
  [[ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]]; then
  echo "release requires main to match origin/main" >&2
  exit 1
fi

current="v0.0.0"
while IFS= read -r tag; do
  if [[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    current="$tag"
    break
  fi
done < <(git tag --list "v*" --sort=-version:refname)

if [[ ! "$current" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  echo "invalid current release tag: $current" >&2
  exit 1
fi
major="${BASH_REMATCH[1]}"
minor="${BASH_REMATCH[2]}"
patch="${BASH_REMATCH[3]}"

case "$part" in
  patch) patch=$((patch + 1)) ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  major) major=$((major + 1)); minor=0; patch=0 ;;
esac

next="v${major}.${minor}.${patch}"
version="${next#v}"
if git rev-parse --quiet --verify "refs/tags/$next" >/dev/null; then
  echo "tag $next already exists" >&2
  exit 1
fi
if ! grep -Eq "^## \[$version\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md; then
  echo "CHANGELOG.md must contain a dated [$version] release section" >&2
  exit 1
fi

signing_key="${RELEASE_SIGNING_KEY:-}"
if [[ -z "$signing_key" ]]; then
  signing_key="$(git config --get user.signingkey || true)"
fi
if [[ -z "$signing_key" ]] ||
  ! gpg --batch --list-secret-keys "$signing_key" >/dev/null 2>&1; then
  echo "release requires a usable OpenPGP secret key" >&2
  echo "set RELEASE_SIGNING_KEY to its fingerprint" >&2
  exit 1
fi

make check

if ! git diff --quiet || ! git diff --cached --quiet ||
  [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
  echo "release checks modified the working tree" >&2
  exit 1
fi

git -c gpg.format=openpgp -c user.signingkey="$signing_key" \
  tag -s "$next" -m "$next"
echo "created local signed tag $next"
echo "review it, then push with: git push origin $next"
