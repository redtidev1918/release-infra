# Changelog

## Unreleased

- Add ReleaseGraph Go read-only core with typed domain models.
- Add declarative release graph validation, topological ordering, ready/blocked sets, cycle and missing-node detection.
- Add Mermaid and versioned JSON graph/plan output.
- Add policy inspection and desired-version planning.
- Add Python/Go contract comparison fixtures.
- Keep the Python workflow as the production mutating release path.

## v1 production baseline

- Reusable pinned release workflow.
- Required asset and registry gates.
- Immutable tag checks and same-version recovery.
- Metadata, checksums, audit, and Release-object-only retention.
- Production fleet dogfood and status dashboard.
