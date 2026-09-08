# Migration

1. Run Fleet Audit and save `migration/snapshots/<repo>.json`.
2. Classify the real product artifact and registry.
3. Add `.release-policy.yml` and an existing build adapter that writes `dist/release/`.
4. Set Release Please `skip-github-release: true`.
5. Replace release workflow duplication with the pinned `@v1` caller.
6. Run `dry_run`, then merge only after canary checks pass.
7. Verify the current public Release before pruning older Release objects.

Forks, private repositories, ambiguous monorepos, and projects whose registry identity cannot be proved remain observe-only/needs-review. Their working release automation is not changed.
