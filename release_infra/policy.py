from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path
from typing import Any


KINDS = {"binary", "python-library", "node-library", "flutter", "android", "container", "hybrid", "none"}
VERSIONING = {"release-please", "manual"}
REGISTRIES = {"github", "pypi", "npm", "pub", "ghcr"}


class PolicyError(ValueError):
    pass


def load_policy(path: str | Path = ".release-policy.yml") -> dict[str, Any]:
    raw = Path(path).read_bytes()
    try:
        policy = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise PolicyError("policy must use JSON syntax (valid YAML 1.2)") from exc
    validate_policy(policy)
    policy["_hash"] = hashlib.sha256(raw).hexdigest()
    return policy


# The asset pattern language.
#
# ReleaseGraph supports literal characters, `*` (any run, including empty) and
# `?` (exactly one character). Everything else is rejected.
#
# The reason is parity, and it is measured rather than assumed. Python's
# `fnmatch` and Go's `path.Match` are NOT the same language: `[!a]` means "not a"
# to fnmatch and "the literal characters ! and a" to path.Match, and `[^a]` means
# the exact opposite pair. CPython 3.14's fnmatch also compiles to atomic groups
# and lookaheads, which Go's RE2 cannot express at all, so porting it is
# impossible. Restricting the language to the region where both engines provably
# agree is what makes the two implementations comparable:
# `testdata/health/patterns.json` pins 484 (pattern, name) pairs that Python and
# Go are both required to match identically.
#
# The restriction costs nothing today: all 48 required patterns across the 16
# managed repositories use only literals and `*`, and none of the 137 released
# asset names contains a reserved character.
RESERVED_PATTERN_CHARS = "\\/[]!^"


def validate_asset_pattern(pattern: str) -> None:
    """Raise PolicyError unless the pattern is inside the supported language."""
    if not pattern:
        raise PolicyError("asset patterns must be non-empty")
    for char in pattern:
        if char in RESERVED_PATTERN_CHARS:
            raise PolicyError(
                f"asset pattern {pattern} uses reserved character {char}; "
                "supported syntax is literals, * and ?"
            )
        if ord(char) < 0x20 or ord(char) == 0x7F:
            raise PolicyError(f"asset pattern {pattern} contains a control character")


def validate_policy(policy: Any) -> None:
    if not isinstance(policy, dict):
        raise PolicyError("policy must be an object")
    if policy.get("kind") not in KINDS:
        raise PolicyError(f"kind must be one of: {', '.join(sorted(KINDS))}")
    versioning = policy.get("versioning")
    if not isinstance(versioning, dict) or versioning.get("mode") not in VERSIONING:
        raise PolicyError("versioning.mode must be release-please or manual")
    assets = policy.get("assets", {})
    for key in ("required", "optional"):
        if not isinstance(assets.get(key, []), list) or not all(isinstance(v, str) and v for v in assets.get(key, [])):
            raise PolicyError(f"assets.{key} must be a list of non-empty strings")
        for pattern in assets.get(key, []):
            validate_asset_pattern(pattern)
    registries = policy.get("registries", {})
    unknown = set(registries) - REGISTRIES
    if unknown:
        raise PolicyError(f"unknown registries: {', '.join(sorted(unknown))}")
    for name, config in registries.items():
        if not isinstance(config, dict) or config.get("required", True) not in (True, False):
            raise PolicyError(f"registries.{name} must be an object with boolean required")
        for command in ("publish", "verify"):
            value = config.get(command, "")
            if not isinstance(value, str) or "\n" in value:
                raise PolicyError(f"registries.{name}.{command} must be a single-line string")
    for name in ("test", "command", "version_check"):
        value = policy.get("build", {}).get(name, "")
        if not isinstance(value, str) or "\n" in value:
            raise PolicyError(f"build.{name} must be a single-line string")
    matrix = policy.get("build", {}).get("matrix")
    if matrix is not None:
        if not isinstance(matrix, list) or not matrix:
            raise PolicyError("build.matrix must be a non-empty array")
        allowed = {"runner", "command", "version_check"}
        for item in matrix:
            if not isinstance(item, dict) or not isinstance(item.get("runner"), str) or not item["runner"]:
                raise PolicyError("each build.matrix item needs a runner string")
            if not isinstance(item.get("command"), str) or "\n" in item["command"]:
                raise PolicyError("each build.matrix item needs a single-line command")
            if set(item) - allowed:
                raise PolicyError(f"unknown build.matrix field(s): {', '.join(sorted(set(item) - allowed))}")
    post_publish = policy.get("release", {}).get("post_publish", "")
    if not isinstance(post_publish, str) or "\n" in post_publish:
        raise PolicyError("release.post_publish must be a single-line string")


def desired_version(policy: dict[str, Any], explicit: str | None = None, root: str | Path = ".") -> str:
    if explicit:
        version = explicit.removeprefix("v")
        if not re.fullmatch(r"[0-9]+(?:\.[0-9A-Za-z-]+)+", version):
            raise PolicyError(f"invalid release version: {explicit}")
        return version
    versioning = policy["versioning"]
    if versioning["mode"] == "manual":
        version = versioning.get("version")
        if not version:
            raise PolicyError("manual versioning requires versioning.version or --version")
        return desired_version({"versioning": {"mode": "manual", "version": None}}, str(version))
    manifest_path = Path(root) / versioning.get("manifest", ".release-please-manifest.json")
    manifest = json.loads(manifest_path.read_text())
    package = versioning.get("package", ".")
    if package not in manifest:
        raise PolicyError(f"manifest has no package {package!r}")
    return desired_version({"versioning": {"mode": "manual", "version": None}}, str(manifest[package]))
