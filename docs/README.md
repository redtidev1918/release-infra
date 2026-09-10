# ReleaseGraph

Serverless, declarative, DAG-driven release orchestration for GitHub Actions.

ReleaseGraph computes release plans from desired state, current GitHub and registry state, project policy, and a dependency graph. Every worker runs in GitHub Actions and exits after planning or dispatching work. No server or central database is required.

[简体中文](zh-CN/) · [GitHub repository](https://github.com/redtidev1918/releasegraph)

## Start here

1. Read the [architecture](ARCHITECTURE.md) and [policy contract](POLICY.md).
2. Copy the [control repository example](https://github.com/redtidev1918/releasegraph/tree/v1/examples/control).
3. Run `releasegraph plan --graph release-graph.yml --live --output json` with read-only access.
4. Review [authentication](AUTHENTICATION.md) before enabling writes.

## Documentation

- [Architecture](ARCHITECTURE.md)
- [Policy](POLICY.md)
- [Calling the release workflow](callers.md)
- [Release health](health.md)
- [Plan contract](plan.md)
- [Authentication](AUTHENTICATION.md)
- [Recovery](RECOVERY.md)
- [Migration](MIGRATION.md)
- [中文快速开始](zh-CN/quick-start.md)
- [中文核心概念](zh-CN/concepts.md)
