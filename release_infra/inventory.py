from __future__ import annotations

import base64
import concurrent.futures
import datetime as dt
import json
import os
import re
import tomllib
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

from . import health
from .github import GitHub, GitHubError
from .policy import PolicyError, validate_production_operations


SIGNALS = {
    "package.json", "pyproject.toml", "setup.py", "pubspec.yaml", "Cargo.toml", "go.mod", "Dockerfile",
    ".release-policy.yml", ".release-please-manifest.json", "release-please-config.json", "CHANGELOG.md",
}


def _content(gh: GitHub, repo: str, path: str) -> str | None:
    try:
        result = gh.api(f"repos/{repo}/contents/{path}")
    except GitHubError:
        return None
    if result.get("encoding") == "base64":
        return base64.b64decode(result["content"]).decode(errors="replace")
    return None


def _release_tags(version: str, policy: dict | None) -> set[str]:
    tags = {version, f"v{version}"}
    template = (policy or {}).get("tag", {}).get("template")
    if template:
        tags.add(template.format(version=version))
    return tags


def _desired_manifest_version(policy: dict | None, manifest_versions: dict) -> str | None:
    package = (policy or {}).get("versioning", {}).get("package", ".")
    return manifest_versions.get(package, next(iter(manifest_versions.values()), None))


def _classify(repo: dict, files: set[str], releases: list[dict]) -> str:
    if repo["archived"]:
        return "archived"
    if repo["fork"]:
        return "fork"
    if ".release-policy.yml" in files:
        return "managed"
    if releases or files & {"package.json", "pyproject.toml", "pubspec.yaml", "Cargo.toml", "go.mod", "Dockerfile"}:
        return "observe-only"
    return "no-release"


def _registry(url: str) -> dict | None:
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            return json.load(response)
    except (OSError, ValueError):
        return None


def _package_status(files: set[str], contents: dict[str, str | None]) -> dict[str, Any]:
    status: dict[str, Any] = {}
    package_json = contents.get("package.json")
    if package_json:
        try:
            package = json.loads(package_json)
            remote = _registry(f"https://registry.npmjs.org/{urllib.parse.quote(package['name'], safe='@')}/latest")
            status["npm"] = {"package": package["name"], "declared": package.get("version"), "published": (remote or {}).get("version")}
        except (KeyError, ValueError):
            pass
    pyproject = contents.get("pyproject.toml")
    if pyproject:
        try:
            project = tomllib.loads(pyproject).get("project", {})
            remote = _registry(f"https://pypi.org/pypi/{urllib.parse.quote(project['name'])}/json")
            status["pypi"] = {"package": project["name"], "declared": project.get("version"), "published": (remote or {}).get("info", {}).get("version")}
        except (KeyError, tomllib.TOMLDecodeError):
            pass
    pubspec = contents.get("pubspec.yaml")
    if pubspec:
        name = re.search(r"(?m)^name:\s*([^\s#]+)", pubspec)
        version = re.search(r"(?m)^version:\s*([^\s+#]+)", pubspec)
        if name:
            remote = _registry(f"https://pub.dev/api/packages/{urllib.parse.quote(name.group(1))}")
            status["pub"] = {"package": name.group(1), "declared": version.group(1) if version else None, "published": (remote or {}).get("latest", {}).get("version")}
    return status


def _branch_contract_status(gh: GitHub, name: str, paths: set[str], parsed_policy: dict | None) -> dict[str, Any]:
    """Report branch-contract governance installation for one repository.

    This is governance compliance only: whether the contract is configured and
    the reusable gate installed and pinned. PR-level ancestry correctness is
    the gate's job at pull-request time; temporary feature branches are never
    scanned and a PR violation is never a repository health failure.
    """
    operations = (((parsed_policy or {}).get("repository") or {}).get("git") or {}).get("productionOperations")
    gate_installed = False
    gate_pinned: bool | None = None
    for path in sorted(p for p in paths if p.startswith(".github/workflows/") and "branch-contract" in p):
        content = _content(gh, name, path) or ""
        refs = re.findall(r"reusable-branch-contract\.yml@([^\s\"']+)", content)
        if not refs:
            continue
        gate_installed = True
        gate_pinned = all(re.fullmatch(r"[0-9a-f]{40}", ref) for ref in refs)
    policy_valid: bool | None = None
    if operations is not None:
        try:
            validate_production_operations(operations)
            policy_valid = True
        except PolicyError:
            policy_valid = False
    return {
        "configured": operations is not None,
        "gateInstalled": gate_installed,
        "gatePinned": gate_pinned,
        "policyValid": policy_valid,
    }


def _scan_repo(source: dict) -> dict[str, Any]:
    gh = GitHub()
    name = source["full_name"]
    branch = source.get("default_branch")
    try:
        tree = gh.api(f"repos/{name}/git/trees/{branch}?recursive=1") if branch else {"tree": []}
        paths = {entry["path"] for entry in tree.get("tree", []) if entry.get("type") == "blob"}
        releases = gh.releases(name)
        tags = gh.tags(name)
        workflows = gh.workflows(name)
        runs = gh.runs(name)
    except GitHubError as exc:
        return {"repo": name, "visibility": source.get("visibility", "private"), "health": "BROKEN", "error": str(exc)}
    top_files = {path for path in paths if "/" not in path and path in SIGNALS}
    workflow_paths = sorted(path for path in paths if path.startswith(".github/workflows/") and path.endswith((".yml", ".yaml")))
    contents = {path: _content(gh, name, path) for path in top_files & {"package.json", "pyproject.toml", "pubspec.yaml", ".release-policy.yml", ".release-please-manifest.json", "release-please-config.json"}}
    policy = contents.get(".release-policy.yml")
    parsed_policy = None
    if policy:
        try:
            parsed_policy = json.loads(policy)
        except ValueError:
            pass
    manifest = contents.get(".release-please-manifest.json")
    desired = None
    manifest_versions = {}
    if manifest:
        try:
            manifest_versions = json.loads(manifest)
            desired = _desired_manifest_version(parsed_policy, manifest_versions)
        except (ValueError, StopIteration):
            pass
    latest = next((release for release in releases if not release.get("draft") and not release.get("prerelease")), None)
    drafts = [release for release in releases if release.get("draft")]
    classification = _classify({"archived": source["archived"], "fork": source["fork"]}, top_files, releases)
    actual_assets = [asset["name"] for asset in latest.get("assets", [])] if latest else []
    # The canonical caller, as documented in docs/callers.md: one caller per
    # managed repository at .github/workflows/release.yml. Matching on words like
    # "release"/"deploy" in a workflow's name used to stand in for this, which
    # asked a different question than internal/fleet does -- and the two answers
    # disagreed about svn-easy-kit. Narrowed to the file that the contract names.
    caller_path = ".github/workflows/release.yml"
    release_workflows = [wf for wf in workflows if str(wf.get("path", "")).endswith(caller_path)]
    release_workflow_ids = {wf["id"] for wf in release_workflows}
    latest_run = next((run for run in runs if run.get("workflow_id") in release_workflow_ids and run.get("head_branch") == branch), None)
    package_status = _package_status(top_files, contents)
    branch_contract = _branch_contract_status(gh, name, paths, parsed_policy)
    latest_tag_name = latest.get("tag_name") if latest else None
    release_matches_desired = not desired or latest_tag_name in _release_tags(desired, parsed_policy)
    assessment = health.evaluate(
        parsed_policy,
        health.Observation(
            release=latest_tag_name,
            draft_release=drafts[0]["tag_name"] if drafts else None,
            tag_drift=not release_matches_desired,
            assets=[{"name": asset["name"], "size": asset.get("size")} for asset in (latest.get("assets", []) if latest else [])],
            run_conclusion=(latest_run or {}).get("conclusion"),
            has_policy=bool(policy),
            policy_parsable=parsed_policy is not None,
            archived=classification == "archived",
            fork=classification == "fork",
            unmanaged=classification == "observe-only",
        ),
    )
    return {
        "repo": name, "default_branch": branch, "visibility": source.get("visibility", "public"),
        "archived": source["archived"], "fork": source["fork"], "template": source.get("is_template", False),
        "classification": classification, "health": assessment.value,
        "health_reasons": [reason.as_dict() for reason in assessment.reasons],
        "missing_assets": list(assessment.missing_assets), "empty_assets": list(assessment.empty_assets),
        # Deliberately no `release_health` here. That field names the planner's
        # workflow decision (`release_infra/release.py`), and the planner computes
        # it from commit-level tag drift and workflow state that this inventory
        # never reads. Emitting our own value under the same name would be two
        # systems claiming one field, which is how contradictory dashboards
        # happen. `health.Health.release_health()` exists to bridge the contract
        # to the planner in tests, not to duplicate the planner's output.
        "release_policy": parsed_policy, "release_infra_version": "v1" if any("redtidev1918/releasegraph/.github/workflows/reusable-release.yml@v1" in (_content(gh, name, path) or "") for path in workflow_paths) else None, "desired_version": desired,
        "latest_release": latest.get("tag_name") if latest else None, "latest_release_at": latest.get("published_at") if latest else None,
        "latest_tag": tags[0]["name"] if tags else None, "draft_releases": [release["tag_name"] for release in drafts],
        "actual_assets": actual_assets, "release_workflows": [{"name": wf["name"], "path": wf["path"]} for wf in release_workflows],
        "latest_release_run": ({"status": latest_run["status"], "conclusion": latest_run["conclusion"], "url": latest_run["html_url"], "updated_at": latest_run["updated_at"]} if latest_run else None),
        "signals": sorted(top_files), "workflow_paths": workflow_paths,
        "release_history": [{"tag": release["tag_name"], "draft": release["draft"], "prerelease": release["prerelease"], "published_at": release.get("published_at"), "assets": [{"name": asset["name"], "size": asset["size"], "sha256": asset.get("digest")} for asset in release.get("assets", [])]} for release in releases],
        "tags": [{"name": tag["name"], "commit": tag["commit"]["sha"]} for tag in tags],
        "release_config": {key: json.loads(value) for key, value in contents.items() if key in {".release-please-manifest.json", "release-please-config.json"} and value},
        "registries": package_status,
        "branchContract": branch_contract,
    }


def scan(owner: str, *, include_private: bool = True) -> list[dict[str, Any]]:
    gh = GitHub()
    repos = gh.api(f"user/repos?affiliation=owner&per_page=100&sort=full_name" if include_private else f"users/{owner}/repos?per_page=100&sort=full_name", paginate=True)
    sources = [repo for repo in repos if repo["owner"]["login"].lower() == owner.lower()]
    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
        inventory = list(executor.map(_scan_repo, sources))
    errors = [item["error"] for item in inventory if item.get("error")]
    if errors:
        raise GitHubError("; ".join(errors[:5]))
    return sorted(inventory, key=lambda item: item["repo"].lower())


def _atomic_write(path: Path, content: str) -> None:
    temporary = path.with_name(f".{path.name}.tmp")
    temporary.write_text(content)
    os.replace(temporary, path)


GENERATED_NOTE = (
    "GENERATED — DO NOT EDIT. Human-readable twin: STATUS.md; "
    "desired/managed inventory of record: fleet.yaml. "
    "Regenerated by .github/workflows/fleet-audit.yml."
)


def write_outputs(inventory: list[dict], output: str | Path = ".") -> None:
    root = Path(output)
    root.mkdir(parents=True, exist_ok=True)
    document = {
        "schema_version": 1,
        "generated_at": dt.datetime.now(dt.UTC).isoformat(),
        "_note": GENERATED_NOTE,
        "repositories": inventory,
    }
    _atomic_write(root / "status.json", json.dumps(document, indent=2, ensure_ascii=False) + "\n")
    snapshots = root / "migration/snapshots"
    snapshots.mkdir(parents=True, exist_ok=True)
    for item in inventory:
        if item.get("visibility") == "public":
            _atomic_write(snapshots / f"{item['repo'].split('/')[-1]}.json", json.dumps(item, indent=2, ensure_ascii=False) + "\n")
    lines = [
        "# Release Fleet Dashboard",
        "",
        "> **GENERATED — DO NOT EDIT.** 本文件由 `.github/workflows/fleet-audit.yml` 运行",
        "> `release_infra/inventory.py` 生成，仅在 fleet 状态发生变化时提交，因此 git 历史",
        "> 就是 fleet 状态的变化记录。desired / managed 清单的唯一事实源是",
        "> [`fleet.yaml`](./fleet.yaml)。机器可读的同源快照见 [`status.json`](./status.json)。",
        "",
        f"Generated: `{document['generated_at']}`",
        "",
        "| Repository | Class | Desired | Latest | Assets | Workflow | Contract | Health |",
        "|---|---|---:|---:|---:|---|---|---|",
    ]
    for item in inventory:
        run = item.get("latest_release_run") or {}
        contract = item.get("branchContract") or {}
        if contract.get("configured") and contract.get("gateInstalled"):
            contract_state = "policy+gate" if contract.get("gatePinned", False) else "policy+gate(unpinned)"
        elif contract.get("configured"):
            contract_state = "policy"
        elif contract.get("gateInstalled"):
            contract_state = "gate"
        else:
            contract_state = "—"
        lines.append(f"| {item['repo']} | {item.get('classification', 'needs-review')} | {item.get('desired_version') or '—'} | {item.get('latest_release') or '—'} | {len(item.get('actual_assets', []))} | {run.get('conclusion') or run.get('status') or '—'} | {contract_state} | {item.get('health', 'NEEDS_REVIEW')} |")
    _atomic_write(root / "STATUS.md", "\n".join(lines) + "\n")
