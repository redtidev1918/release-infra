from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class ReleaseState:
    desired: str
    tag_commit: str | None = None
    expected_commit: str | None = None
    release: dict | None = None
    required_assets: tuple[str, ...] = ()
    registries_ok: bool = True
    latest_tag: str | None = None
    prerelease: bool = False


def next_action(state: ReleaseState) -> str:
    tag = f"v{state.desired}"
    if state.tag_commit and state.expected_commit and state.tag_commit != state.expected_commit:
        return "FAIL_TAG_CONFLICT"
    if not state.release:
        return "CREATE_DRAFT" if state.tag_commit else "BUILD"
    assets = {asset["name"] for asset in state.release.get("assets", []) if asset.get("size", 0) > 0}
    if not set(state.required_assets).issubset(assets):
        return "UPLOAD_ASSETS" if state.release.get("draft") else "FAIL_PUBLIC_INCOMPLETE"
    if not state.registries_ok:
        return "VERIFY_REGISTRIES" if state.release.get("draft") else "FAIL_REGISTRY_DRIFT"
    if state.release.get("draft"):
        return "PUBLISH"
    if not state.prerelease and state.latest_tag != tag:
        return "SET_LATEST"
    return "AUDIT"
