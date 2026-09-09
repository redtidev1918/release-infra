# Contributing

1. Preserve release safety: no tag rebinding, no `--cleanup-tag`, no version bump to hide an infrastructure failure.
2. Keep the core owner-agnostic. User fleet names belong in a separate control repository, examples, or anonymized fixtures.
3. Prefer Go stdlib and built-in interfaces over a plugin framework.
4. Add the smallest test that fails if a graph, policy, recovery, or integrity invariant breaks.
5. Run:

```sh
go vet ./...
go test ./...
python3 -m unittest discover
scripts/contract-compare
```

Mutating release behavior requires a dry-run canary before changing the stable `v1` channel.
