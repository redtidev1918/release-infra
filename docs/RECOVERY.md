# Recovery

Scheduled callers compare the manifest version with the tag, draft/public Release, required assets, registries, and Latest. A healthy version is a no-op. An absent or draft version resumes the same transaction; no new version is calculated.

Run the repository's **Release** workflow manually with:

- `version`: an existing manifest version; blank reads the manifest. Branch names are rejected.
- `dry_run`: build and validate without public mutations.
- `repair`: allow an incomplete public Release to enter explicit repair.
- `force`: rerun a healthy version for audit/build diagnostics.
- `stage`: reserved recovery label recorded in the Job Summary.

Tag/asset/registry hash conflicts are deterministic hard failures. Network and API failures use bounded exponential retry. Never move or delete a historical semver tag to make recovery pass.
