# Release Infrastructure v1

One release protocol for `redtidev1918` repositories: Release Please proposes versions; the reusable workflow tests and builds before creating a draft, gates required assets, publishes registries, verifies and publishes GitHub Release, sets Latest, audits, then prunes Release objects without deleting tags.

## Daily use

1. Write conventional commits.
2. Merge the Release Please PR.
3. The repository watchdog resumes the same version after a transient failure.

Manual recovery: **Actions → Release → Run workflow**, select `repair`, and optionally enter the existing version. Fleet health is in [STATUS.md](STATUS.md); machine-readable state is [status.json](status.json).

Policies use JSON syntax in `.release-policy.yml` (JSON is valid YAML 1.2), keeping the runner dependency-free. See [POLICY.md](docs/POLICY.md).
