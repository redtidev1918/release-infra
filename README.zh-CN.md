# ReleaseGraph

面向 GitHub Actions 的无服务器、声明式、DAG 驱动多仓库发布编排器。

```text
        core
       /    \
     cli    web
       \    /
      deploy
```

ReleaseGraph 根据期望状态、GitHub 与 registry 实际状态、项目 policy 和依赖图生成发布计划，验证 Release，并恢复未完成的同版本事务。

不需要服务器，不需要数据库，不运行轮询 daemon。

[English](README.md) · [中文文档站](https://redtidev1918.github.io/release-infra/zh-CN/) · [中文快速开始](docs/zh-CN/quick-start.md)

## 当前状态

Go 核心目前开放只读 canary：

```bash
go build -o releasegraph ./cmd/releasegraph
./releasegraph doctor
./releasegraph graph --file examples/control/release-graph.yml --format mermaid
./releasegraph plan --graph release-graph.yml --live --output json
```

Python reusable workflow 仍负责已经过生产验证的写路径。Go registry 检查、dispatch、repair 与 release transaction 完成契约对比和 canary 前，不标记为稳定能力。

## 项目边界

ReleaseGraph 负责 desired/actual state、DAG 调度、发布完整性、registry 验证、恢复与审计。具体仓库继续负责自己的 test/build，并把候选资产写入 `dist/release/`。

事件只触发 reconcile，不代表事实，也不会强制下游自动 bump version。
