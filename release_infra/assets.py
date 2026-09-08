from __future__ import annotations

import glob
import hashlib
import tarfile
import zipfile
from pathlib import Path


class AssetError(ValueError):
    pass


def collect_assets(required: list[str], optional: list[str], root: str | Path = "dist/release") -> list[Path]:
    base = Path(root)
    found: list[Path] = []
    missing: list[str] = []
    for pattern in required:
        matches = [Path(p) for p in glob.glob(str(base / pattern)) if Path(p).is_file()]
        if not matches:
            missing.append(pattern)
        found.extend(matches)
    for pattern in optional:
        found.extend(Path(p) for p in glob.glob(str(base / pattern)) if Path(p).is_file())
    if missing:
        raise AssetError(f"missing required assets: {', '.join(missing)}")
    unique = sorted(set(found))
    for path in unique:
        if path.stat().st_size == 0:
            raise AssetError(f"empty asset: {path}")
        if path.suffix == ".zip" and not zipfile.is_zipfile(path):
            raise AssetError(f"invalid zip asset: {path}")
        if any(path.name.endswith(suffix) for suffix in (".tar", ".tar.gz", ".tgz", ".tar.xz")) and not tarfile.is_tarfile(path):
            raise AssetError(f"invalid tar asset: {path}")
    return unique


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_checksums(assets: list[Path], output: str | Path = "dist/release/SHA256SUMS") -> Path:
    path = Path(output)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("".join(f"{sha256(asset)}  {asset.name}\n" for asset in assets))
    return path
