from __future__ import annotations

import datetime as dt
import json
import os
import subprocess
import tempfile
from pathlib import Path

from . import __version__
from .assets import collect_assets, sha256, write_checksums
from .github import GitHubError
from .policy import desired_version, load_policy
from .retry import retry


class ReleaseError(RuntimeError):
    pass


def _run(command: list[str], *, capture: bool = False) -> str:
    def invoke() -> str:
        result = subprocess.run(command, text=True, capture_output=True)
        if result.returncode:
            raise ReleaseError(result.stderr.strip() or result.stdout.strip() or f"command failed: {' '.join(command)}")
        if not capture and result.stdout:
            print(result.stdout, end="")
        return result.stdout.strip() if capture else ""

    transient = ("timed out", "timeout", "connection reset", "temporary failure", "http 5", "rate limit", "secondary rate")
    return retry(invoke, lambda exc: command[0] in {"gh", "git"} and any(text in str(exc).lower() for text in transient))


def _release(tag: str) -> dict | None:
    result = subprocess.run(
        ["gh", "release", "view", tag, "--json", "id,tagName,isDraft,isPrerelease,assets,url"],
        text=True, capture_output=True,
    )
    if result.returncode:
        return None
    release = json.loads(result.stdout)
    latest = subprocess.run(["gh", "release", "view", "--json", "tagName"], text=True, capture_output=True)
    release["isLatest"] = not latest.returncode and json.loads(latest.stdout)["tagName"] == tag
    return release


def _remote_tag_commit(tag: str) -> str | None:
    result = subprocess.run(["git", "ls-remote", "--tags", "origin", f"refs/tags/{tag}^{{}}", f"refs/tags/{tag}"], text=True, capture_output=True, check=True)
    lines = result.stdout.splitlines()
    peeled = next((line.split()[0] for line in lines if line.endswith("^{}")), None)
    return peeled or (lines[0].split()[0] if lines else None)


def _ensure_tag(tag: str, commit: str, *, dry_run: bool = False) -> None:
    remote = _remote_tag_commit(tag)
    if remote and remote != commit:
        raise ReleaseError(f"tag {tag} points to {remote}, expected {commit}")
    if not remote and not dry_run:
        _run(["git", "tag", "-a", tag, commit, "-m", f"Release {tag}"])
        _run(["git", "push", "origin", f"refs/tags/{tag}"])


def _upload_idempotent(tag: str, paths: list[Path], *, dry_run: bool = False) -> None:
    current = _release(tag)
    remote = {asset["name"]: asset for asset in (current or {}).get("assets", [])}
    with tempfile.TemporaryDirectory() as directory:
        for path in paths:
            if path.name in remote:
                _run(["gh", "release", "download", tag, "--pattern", path.name, "--dir", directory])
                downloaded = Path(directory) / path.name
                if sha256(downloaded) != sha256(path):
                    raise ReleaseError(f"remote asset differs: {path.name}")
                downloaded.unlink()
                continue
            if not dry_run:
                _run(["gh", "release", "upload", tag, str(path)])


def stage(policy_path: str = ".release-policy.yml", version: str | None = None, *, dry_run: bool = False) -> str:
    policy = load_policy(policy_path)
    desired = desired_version(policy, version)
    tag = policy.get("tag", {}).get("template", "v{version}").format(version=desired)
    commit = _run(["git", "rev-parse", "HEAD"], capture=True)
    assets = collect_assets(policy.get("assets", {}).get("required", []), policy.get("assets", {}).get("optional", []))
    checksums = write_checksums(assets)
    if dry_run:
        return tag
    _ensure_tag(tag, commit, dry_run=dry_run)
    release = _release(tag)
    if release and not release["isDraft"]:
        raise ReleaseError(f"{tag} is already public; run audit instead")
    if not release and not dry_run:
        _run(["gh", "release", "create", tag, "--verify-tag", "--draft", "--title", tag, "--generate-notes"])
    _upload_idempotent(tag, [*assets, checksums], dry_run=dry_run)
    return tag


def plan(policy_path: str = ".release-policy.yml", version: str | None = None, *, force: bool = False, repair: bool = False) -> dict[str, str]:
    policy = load_policy(policy_path)
    desired = desired_version(policy, version)
    tag = policy.get("tag", {}).get("template", "v{version}").format(version=desired)
    release = _release(tag)
    required = set(policy.get("assets", {}).get("required", [])) | {"SHA256SUMS", "RELEASE-METADATA.json"}
    remote = {asset["name"] for asset in (release or {}).get("assets", []) if int(asset.get("size", 0)) > 0}
    healthy = bool(release and not release["isDraft"] and required.issubset(remote))
    needs_repair = bool(release and not release["isDraft"] and not healthy)
    registries = policy.get("registries", {})
    ghcr = registries.get("ghcr", {})
    retry_count = 0
    cooldown = False
    if os.environ.get("GITHUB_EVENT_NAME") == "schedule":
        try:
            runs = json.loads(_run(["gh", "run", "list", "--limit", "10", "--json", "conclusion,createdAt,event"], capture=True))
            failures = [run for run in runs if run.get("conclusion") == "failure"]
            retry_count = len(failures)
            if retry_count >= 3:
                last = dt.datetime.fromisoformat(failures[0]["createdAt"].replace("Z", "+00:00"))
                cooldown = dt.datetime.now(dt.UTC) - last < dt.timedelta(hours=6)
        except (ReleaseError, ValueError, KeyError):
            pass
    should_release = force or repair or (not healthy and not needs_repair and not cooldown)
    return {
        "should_release": str(should_release).lower(), "run_release": "1" if should_release else "0", "version": desired, "tag": tag,
        "retry_count": str(retry_count), "cooldown": str(cooldown).lower(), "needs_repair": str(needs_repair).lower(),
        "test_command": policy.get("build", {}).get("test", ""), "build_command": policy.get("build", {}).get("command", ""),
        "version_check": policy.get("build", {}).get("version_check", ""),
        "pypi_enabled": str("pypi" in registries).lower(), "pypi_required": str(registries.get("pypi", {}).get("required", True)).lower(),
        "pypi_packages_dir": registries.get("pypi", {}).get("packages_dir", "dist/release"),
        "ghcr_enabled": str("ghcr" in registries).lower(), "ghcr_required": str(ghcr.get("required", True)).lower(),
        "ghcr_context": ghcr.get("context", "."), "ghcr_file": ghcr.get("file", "Dockerfile"),
        "ghcr_image": ghcr.get("image", ""), "ghcr_platforms": ghcr.get("platforms", "linux/amd64"),
        "required_publish": " && ".join(config.get("publish", ":") for name, config in registries.items() if name != "ghcr" and config.get("required", True) and config.get("publish")),
        "required_verify": " && ".join(config.get("verify", ":") for config in registries.values() if config.get("required", True) and config.get("verify")),
        "optional_publish": "; ".join(f"({config['publish']}) || true" for name, config in registries.items() if name != "ghcr" and not config.get("required", True) and config.get("publish")),
        "optional_verify": "; ".join(f"({config['verify']}) || true" for config in registries.values() if not config.get("required", True) and config.get("verify")),
        "post_publish": policy.get("release", {}).get("post_publish", ""),
    }


def _metadata(policy: dict, version: str, tag: str, assets: list[Path]) -> dict:
    started = os.environ.get("RELEASE_BUILD_STARTED_AT") or dt.datetime.now(dt.UTC).isoformat()
    sha = os.environ.get("GITHUB_SHA") or _run(["git", "rev-parse", "HEAD"], capture=True)
    return {
        "repository": os.environ.get("GITHUB_REPOSITORY"), "version": version, "tag": tag, "commit_sha": sha,
        "build_run_id": os.environ.get("GITHUB_RUN_ID"), "build_run_url": f"{os.environ.get('GITHUB_SERVER_URL', 'https://github.com')}/{os.environ.get('GITHUB_REPOSITORY')}/actions/runs/{os.environ.get('GITHUB_RUN_ID')}",
        "release_infra_version": __version__, "release_policy_hash": policy["_hash"], "build_started_at": started,
        "published_at": dt.datetime.now(dt.UTC).isoformat(), "assets": [path.name for path in assets],
        "asset_sha256": {path.name: sha256(path) for path in assets}, "registries": policy.get("registries", {}),
        "container_digest": os.environ.get("CONTAINER_DIGEST"), "package_versions": {name: version for name in policy.get("registries", {})},
        "source_commit": sha, "release_pr": os.environ.get("RELEASE_PR"), "retry_count": int(os.environ.get("RELEASE_RETRY_COUNT", "0")),
    }


def publish(policy_path: str = ".release-policy.yml", version: str | None = None, *, dry_run: bool = False) -> str:
    policy = load_policy(policy_path)
    desired = desired_version(policy, version)
    tag = policy.get("tag", {}).get("template", "v{version}").format(version=desired)
    assets = collect_assets(policy.get("assets", {}).get("required", []), policy.get("assets", {}).get("optional", []))
    metadata_path = Path("dist/release/RELEASE-METADATA.json")
    metadata_path.write_text(json.dumps(_metadata(policy, desired, tag, assets), indent=2) + "\n")
    _upload_idempotent(tag, [metadata_path], dry_run=dry_run)
    if dry_run:
        return tag
    prerelease = bool(policy.get("release", {}).get("prerelease", False))
    command = ["gh", "release", "edit", tag, "--draft=false", f"--prerelease={'true' if prerelease else 'false'}"]
    if not prerelease:
        command.append("--latest")
    _run(command)
    audit(policy_path, desired)
    prune(policy_path)
    return tag


def audit(policy_path: str = ".release-policy.yml", version: str | None = None) -> None:
    policy = load_policy(policy_path)
    desired = desired_version(policy, version)
    tag = policy.get("tag", {}).get("template", "v{version}").format(version=desired)
    expected_commit = os.environ.get("GITHUB_SHA") or _run(["git", "rev-parse", "HEAD"], capture=True)
    if _remote_tag_commit(tag) != expected_commit:
        raise ReleaseError(f"tag commit mismatch for {tag}")
    release = _release(tag)
    if not release or release["isDraft"]:
        raise ReleaseError(f"public release missing for {tag}")
    remote = {asset["name"]: asset for asset in release["assets"]}
    required = set(policy.get("assets", {}).get("required", [])) | {"SHA256SUMS", "RELEASE-METADATA.json"}
    missing = [name for name in required if name not in remote or int(remote[name].get("size", 0)) <= 0]
    if missing:
        raise ReleaseError(f"release assets missing or empty: {', '.join(sorted(missing))}")
    if not release["isPrerelease"] and not release["isLatest"]:
        raise ReleaseError(f"{tag} is not latest")


def prune(policy_path: str = ".release-policy.yml") -> None:
    policy = load_policy(policy_path)
    retention = policy.get("retention", {})
    keep_stable = int(retention.get("stable", 1))
    keep_prerelease = int(retention.get("prerelease", 1))
    releases = json.loads(_run(["gh", "release", "list", "--limit", "100", "--json", "tagName,isDraft,isPrerelease,publishedAt"], capture=True))
    stable = [release for release in releases if not release["isDraft"] and not release["isPrerelease"]]
    prereleases = [release for release in releases if not release["isDraft"] and release["isPrerelease"]]
    for release in stable[keep_stable:] + prereleases[keep_prerelease:]:
        _run(["gh", "release", "delete", release["tagName"], "--yes"])


def assert_no_cleanup_tag(root: str | Path = ".") -> None:
    for path in Path(root).rglob("*"):
        if path.is_file() and (".github/workflows" in str(path) or "scripts" in path.parts):
            try:
                if "--cleanup-tag" in path.read_text(errors="ignore"):
                    raise ReleaseError(f"forbidden --cleanup-tag in {path}")
            except OSError:
                pass
