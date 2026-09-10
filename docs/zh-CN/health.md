# 发布健康

健康只回答一个问题：**已发布的状态是否满足 policy？** 它刻意不是一条指令。该采取什么动作是另一条轴；把两者混为一谈，正是让整个机队反复重发一个其实只是打错标签的版本的原因。

同一套词表由三处描述，`tests/test_health.py` 保证它们不漂移，任一处改错都会让测试失败：

| 层 | 定义位置 |
|---|---|
| Python 机队盘点 | `release_infra/health.py` |
| Go 核心 | `internal/domain/health.go`，取值来自 `internal/domain/model.go` |
| 发布的 schema | [`schemas/health-v1.json`](../../schemas/health-v1.json) |

## 取值

一个仓库只会被报成下面其中一个。没有 `UNKNOWN`：读不到 provider 状态就是 `BROKEN`，那是一个事实，不是含糊其辞。

| 取值 | 含义 |
|---|---|
| `HEALTHY` | 已发布状态满足 policy，不携带任何原因。 |
| `DEGRADED` | 发布存在，但某个不变量不成立。具体是哪一个由原因码说明。 |
| `NO_RELEASE` | 目标版本没有非 draft 的发布，或仓库已归档／是 fork。 |
| `UNMANAGED` | 仓库没有声明发布 policy，ReleaseGraph 不管它。 |
| `BROKEN` | 读不到 provider 状态。 |
| `BLOCKED` | 缺依赖或凭据，暂时无法判断。 |
| `NEEDS_REVIEW` | 需要人来决定。 |

`internal/domain.Health` 比这张表更宽：`READY`、`RUNNING`、`NOOP`、`ACK_PENDING`、`WAIVED` 描述的是发布**事务**中的节点，不是仓库。`domain.RepoHealthValues` 才是能描述仓库的子集，也就是上面这张表。

## 原因

除 `HEALTHY` 外，每个取值都至少带一个原因。原因是机器可读的 code 加一段可选的人类可读 detail，所以看板能按成因分组，人也能直接看到是哪个 tag 或哪个模式出了问题。

| Code | 触发条件 |
|---|---|
| `unmanaged_repo` | 没有声明发布 policy。 |
| `api_error` | 读不到 provider 状态。 |
| `release_absent` | 目标版本没有非 draft 的发布。 |
| `release_draft` | 存在 draft 发布但没有正式发布。 |
| `repository_archived` / `repository_fork` | 仓库已归档／是 fork。 |
| `version_drift` | 发布 tag 不是目标版本。 |
| `target_assets_missing` | 某个必需资产模式什么都没匹配到。 |
| `asset_empty` | 某个必需资产模式只匹配到零字节资产。 |
| `release_run_failed` | 发布工作流运行没有成功。 |
| `policy_absent` / `policy_unparsable` | policy 缺失或读不出来。 |
| `dependency_unhealthy` | 依赖不健康。 |
| `fleet_credential_required` | fleet 作用域需要 `RELEASEGRAPH_FLEET_TOKEN`。 |
| `manual_review_required` | 需要人来决定。 |

`target_assets_missing` 和 `asset_empty` 分开，是因为修法不同：前者要重新上传资产，后者要修一个产出了空文件的构建。

## 资产比对

机队视图与发布闸门用同一个函数（`fnmatch`）问同一个问题，所以它们不可能对同一个发布给出不同结论：

```
required = policy.assets.required
         + RELEASE-METADATA.json
         + SHA256SUMS            # 仅在 checksums 开启（默认）且 policy 声明了 required 时
```

一个必需模式只有被 `size > 0` 的匹配资产满足才算满足。什么都没匹配到是**缺失**；只匹配到零字节资产是**空**。发布规划器把两者都算作"不存在"——它的远端集合由 `size > 0` 的资产构成——所以 `missing + empty` 恰好等于规划器认定的缺失集合，这一点由 `PlannerParityTest` 对**真实规划器**断言，而不是对它的副本断言。

## 健康不是动作

`release_health` 属于发布规划器（`release_infra/release.py`）。它是**工作流决策**——"发布流水线该做什么"——规划器用提交级的 tag 漂移、draft 状态和工作流状态来算它，而这些机队盘点从来不读。因此**机队盘点根本不输出 `release_health`**。两个系统用不同输入发布同名字段，正是矛盾看板的来源；一个字段只能有一个主人。

契约仍然需要知道健康会如何投影到那套词表上，因为 `PlannerParityTest` 正是用这个投影把契约与规划器的真实输出对上。投影关系是：

| 健康 | `release_health` |
|---|---|
| `HEALTHY` | `healthy` |
| `DEGRADED` | `repair` |
| `BLOCKED` | `repair` |
| `NEEDS_REVIEW` | `repair` |
| `NO_RELEASE` | `missing` |
| `BROKEN` | `missing` |
| `UNMANAGED` | `missing` |

`reusable-release.yml` 用 `release_health != 'healthy'` 做判断，且有 14 个仓库调用该工作流，所以这四个 `release_health` 字符串是冻结的生产契约：只能增加取值，不能删改任何一个。任何不是明确 `HEALTHY` 的状态都映射到最响的那个取值，因此未被识别的状态永远不会被当成健康的。

规划器内部的优先级是 `tag-drift` → `repair` → `missing`，其中 `tag-drift` 优先：机队视图无法复现这个标签，因为那里的 tag 漂移指的是"已发布的提交不等于当前 head"，而盘点从不检查提交。两个视图仍然在唯一重要的事情上一致——这个仓库健不健康。

为什么是 `DEGRADED` 而不是为"发布存在但资产不存在"另造一个取值：`internal/provider/capabilities.go` 早就把"缺必需二进制"报成 `DEGRADED`。为同一状况再发明一个词，恰恰会重新制造这份契约本来要消除的矛盾。区分度放在原因码里。

## 怎么读

```bash
releasegraph provider inspect --all --manifest fleet.yaml            # 逐个仓库
python3 -m release_infra.cli fleet-audit --owner redtidev1918        # 重新生成 status.json + STATUS.md
```

`status.json` 为每个仓库给出 `health`、`health_reasons`、`missing_assets`、`empty_assets` 以及投影后的 `release_health`。`STATUS.md` 渲染健康列；原因见机队看板。

## 已知分歧

记在这里，而不是掩盖过去。词表已经统一；实现还没有同样完整，每一处缺口都有归属。

| 分歧 | 影响 | 记录位置 |
|---|---|---|
| `internal/fleet` 只赋 `UNMANAGED`、`NO_RELEASE`、`NEEDS_REVIEW`，其 `enrichReleaseHealth` 从不计算健康，所以被管理的仓库在 Go 里永远停在 `NEEDS_REVIEW` | `releasegraph fleet` 报告不足：它说不出 `HEALTHY` 或 `DEGRADED` | `internal/domain/health.go`；下一片是让 Go 的 fleet 做同样的资产比对 |
| `provider.Verdict` 不携带 `reasons` | Go 侧只能看到取值、看不到原因；Python 盘点已经带原因 | `internal/provider/provider.go` |
| `provider.Verdict.Health` 用同一个字段名表示**漂移判定**轴，其中包含 `RECOVERABLE`——那是一个可采取的动作，不是一个状态 | 正是这份契约要消除的"健康即动作"混淆 | `internal/provider/provider.go`；该取值不得作为仓库健康上报 |

`tests/test_health.py` 强制约束今天能被约束的部分：Python 盘点的取值、Go 的仓库健康赋值、schema 枚举、两种语言的原因码，以及规划器使用的投影关系。
