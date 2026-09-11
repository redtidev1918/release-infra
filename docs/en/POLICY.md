# Release policy

Each managed repository has one `.release-policy.yml`, validated against [`schemas/release-policy.schema.json`](../schemas/release-policy.schema.json). The file uses JSON syntax to avoid a YAML runtime dependency.

```json
{
  "kind": "binary",
  "versioning": {"mode": "release-please"},
  "build": {
    "test": "go test ./...",
    "command": "./scripts/build-release",
    "version_check": "dist/release/app-linux-amd64 --version | grep -Fx $RELEASE_VERSION"
  },
  "assets": {"required": ["app-linux-amd64"], "optional": []},
  "registries": {"github": {"required": true}},
  "retention": {"stable": 1, "prerelease": 1, "failed_draft": 3},
  "checksums": true,
  "sbom": false
}
```

Required asset names are exact contracts. Libraries may declare wheels/sdists or an empty GitHub asset list when the registry package is the product. Container-only projects may require GHCR and no download asset. Commands are single-line repository-owned build adapters; outputs belong in `dist/release/`.

Release Please configs must set `skip-github-release: true`; it manages conventional commits, changelog, versions, manifest, and the Release PR only.
