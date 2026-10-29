# Releasing

A release is a tag. Pushing `v<major>.<minor>.<patch>` from `main` runs
`.github/workflows/release.yml`, which:

1. builds static `railswitch-linux-amd64` and `railswitch-linux-arm64`
   (`make build VERSION=<tag>`, so `railswitch version` prints the tag),
   and `SHA256SUMS`;
2. creates the GitHub release with notes generated from the merged PRs.

Tag a commit whose CI passed: the release workflow doesn't run the tests.

To cut one:

```bash
git checkout main && git pull
make check
git tag -a v0.1.1 -m v0.1.1
git push origin v0.1.1
```

Versions stay below 1.0 while commands and files still change: a minor
release may change them (its notes say how), a patch release only fixes.
A change to a file's format bumps its `schema` (D39), which older
versions refuse rather than misread.
