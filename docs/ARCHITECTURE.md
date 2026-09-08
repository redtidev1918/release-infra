# Architecture

The control plane is public, read-only across the fleet, and owns no cross-repository write credential. Each thin caller executes the pinned `v1` reusable workflow with that repository's `GITHUB_TOKEN`.

```text
Release Please PR -> manifest desired version
                           |
                 TEST -> BUILD -> ASSET GATE
                           |
                   tag -> DRAFT -> assets
                           |
                  required registries
                           |
                 PUBLIC -> LATEST -> AUDIT -> prune Release objects
```

Build, deterministic validation, and asset generation happen before any tag or public object. A failed upload or registry leaves a draft that the same-version run resumes. Optional publication commands never gate required channels. Tags are immutable history; retention deletes only GitHub Release objects.
