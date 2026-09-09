# ReleaseGraph 中文文档

ReleaseGraph 是一个面向 GitHub Actions 的无服务器、多仓库发布编排器。

它读取期望版本、GitHub 与 registry 的实际状态、项目发布策略和依赖 DAG，判断哪些项目可以发布、哪些被阻塞、哪些可以原版本恢复。每次运行都在 GitHub Actions 中完成并退出，不需要常驻服务器或中央数据库。

[English](../) · [GitHub 仓库](https://github.com/redtidev1918/releasegraph)

## 从这里开始

- [快速开始](quick-start.md)：先以只读方式生成 live plan。
- [核心概念](concepts.md)：Desired/Actual、事件、DAG 与恢复语义。
- [认证与权限](authentication.md)：审计、单仓库发布、跨仓库编排三级权限。
- [英文架构文档](../ARCHITECTURE.md)
- [英文 Policy 参考](../POLICY.md)
- [英文恢复说明](../RECOVERY.md)

当前 Go 核心仍处于只读 canary 阶段；Go 写操作、registry 检查与 dispatch 尚未标为稳定能力。
