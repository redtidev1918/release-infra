# Release health

Health answers exactly one question: **does the released state satisfy the
policy?** It is deliberately not an instruction. The action to take is a
different axis, and confusing the two is how a fleet ends up re-releasing
something that was only ever mis-tagged.

Three layers describe this one vocabulary, and they are kept in agreement by
`tests/test_health.py`, which fails when any of them drifts:

| Layer | Definition |
|---|---|
| Python fleet inventory | `release_infra/health.py` |
| Go core | `internal/domain/health.go`, values from `internal/domain/model.go` |
| Published schema | [`schemas/health-v1.json`](../schemas/health-v1.json) |

## Values

A repository is reported as exactly one of these. There is no `UNKNOWN`: an
unreadable provider is `BROKEN`, which is a fact, not a shrug.

| Value | Meaning |
|---|---|
| `HEALTHY` | The released state satisfies the policy. Carries no reasons. |
| `DEGRADED` | A release exists but an invariant fails. The reason codes say which. |
| `NO_RELEASE` | No non-draft release for the desired version, or the repository is archived or a fork. |
| `UNMANAGED` | The repository declares no release policy, so ReleaseGraph does not manage it. |
| `BROKEN` | Provider state could not be read. |
| `BLOCKED` | A dependency or credential is missing, so no judgement is possible yet. |
| `NEEDS_REVIEW` | A human must decide. |

`internal/domain.Health` is wider than this list: `READY`, `RUNNING`, `NOOP`,
`ACK_PENDING` and `WAIVED` describe a node in a release *transaction*, not a
repository. `domain.RepoHealthValues` is the subset that can describe a
repository, and it is the subset above.

## Reasons

Every value except `HEALTHY` carries at least one reason. Reasons are
machine-readable codes with an optional human-readable detail, so a dashboard
can group by cause while a human still sees which tag or pattern is at fault.

| Code | Raised when |
|---|---|
| `unmanaged_repo` | No release policy is declared. |
| `api_error` | Provider state could not be read. |
| `release_absent` | No non-draft release for the desired version. |
| `release_draft` | A draft release exists and was not published. |
| `repository_archived` / `repository_fork` | The repository is archived or a fork. |
| `version_drift` | The release tag is not the desired version. |
| `target_assets_missing` | A required asset pattern matched nothing. |
| `asset_empty` | A required asset pattern matched only zero-byte assets. |
| `release_run_failed` | The release workflow run did not succeed. |
| `policy_absent` / `policy_unparsable` | The policy is missing or not readable. |
| `dependency_unhealthy` | A dependency is not healthy. |
| `fleet_credential_required` | Fleet scope needs `RELEASEGRAPH_FLEET_TOKEN`. |
| `manual_review_required` | A human must decide. |

`target_assets_missing` and `asset_empty` are separate codes because they need
different fixes: re-upload the asset, or fix an upload that produced nothing.

## The asset comparison

Both the fleet view and the release gate ask the same question with the same
function (`fnmatch`), so they cannot disagree about the same release:

```
required = policy.assets.required
         + RELEASE-METADATA.json
         + SHA256SUMS            # only when checksums are enabled (default)
                                 # and the policy declares required assets
```

A required pattern is satisfied only by a matching asset with `size > 0`. A
pattern that matches nothing is *missing*; a pattern that matches only
zero-byte assets is *empty*. The release planner collapses both into "not
present" — it builds its remote set from assets with `size > 0` — so
`missing + empty` equals the planner's missing set exactly, which
`PlannerParityTest` asserts against the real planner rather than a copy of it.

## Health is not an action

`release_health` belongs to the release planner (`release_infra/release.py`).
It is the *workflow decision* — "what should the release pipeline do?" — and the
planner computes it from commit-level tag drift, drafts, and workflow state that
the fleet inventory never reads. **The fleet inventory therefore does not emit
`release_health` at all.** Two systems publishing the same field name from
different inputs is how contradictory dashboards happen, so one field, one owner.

The contract still has to know how health would project onto that vocabulary,
because that projection is how `PlannerParityTest` compares the contract against
the planner's real output. Health projects as:

| Health | `release_health` |
|---|---|
| `HEALTHY` | `healthy` |
| `DEGRADED` | `repair` |
| `BLOCKED` | `repair` |
| `NEEDS_REVIEW` | `repair` |
| `NO_RELEASE` | `missing` |
| `BROKEN` | `missing` |
| `UNMANAGED` | `missing` |

`reusable-release.yml` compares `release_health != 'healthy'` and 14
repositories call that workflow, so the four `release_health` strings are a
frozen production contract: they may gain values, never lose or rename one.
Anything that is not a clear `HEALTHY` maps to the loudest applicable value, so
an unrecognised state can never be mistaken for a healthy one.

Inside the planner the precedence is `tag-drift` → `repair` → `missing`, and
`tag-drift` wins: a fleet view cannot reproduce that label, because tag drift
there means "the released commit is not the current head", which the inventory
never inspects. Both views still agree on the only thing that matters — whether
the repository is healthy.

Why `DEGRADED` and not a separate value for "release exists, assets do not":
`internal/provider/capabilities.go` already reports missing required binaries as
`DEGRADED`. A second word for the same condition would have recreated exactly
the contradiction this contract exists to remove. The specificity lives in the
reason code.

## Reading it

```bash
releasegraph provider inspect --all --manifest fleet.yaml            # per repository
python3 -m release_infra.cli fleet-audit --owner redtidev1918        # regenerates status.json + STATUS.md
```

`status.json` carries `health`, `health_reasons`, `missing_assets`,
`empty_assets` and the projected `release_health` for every repository.
`STATUS.md` renders the health column; see the fleet dashboard for the reasons.

## Known divergences

Recorded here rather than papered over. The vocabulary is unified; the
implementations are not yet equally complete, and each gap has an owner.

| Divergence | Impact | Where it is tracked |
|---|---|---|
| `internal/fleet` assigns only `UNMANAGED`, `NO_RELEASE` and `NEEDS_REVIEW`, and its `enrichReleaseHealth` never computes health, so a managed repository stays `NEEDS_REVIEW` in Go | `releasegraph fleet` under-reports: it cannot say `HEALTHY` or `DEGRADED` | `internal/domain/health.go`; next slice is to give Go's fleet the same asset comparison |
| `provider.Verdict` carries no `reasons` | Go consumers see a value without a cause; the Python inventory carries reasons already | `internal/provider/provider.go` |
| `provider.Verdict.Health` uses the same field name for the *drift verdict* axis, including `RECOVERABLE` — an available action, not a state | the exact "health is an action" confusion this contract exists to remove | `internal/provider/provider.go`; the value must not be reported as repository health |

`tests/test_health.py` enforces the parts that can be enforced today: the Python
inventory's values, Go's repository-health assignments, the schema enum, the
reason codes in both languages, and the projection the planner uses.
