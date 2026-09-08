import json
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path

from release_infra.assets import AssetError, collect_assets, write_checksums
from release_infra.policy import PolicyError, desired_version, load_policy, validate_policy
from release_infra.release import ReleaseError, assert_no_cleanup_tag


POLICY = {
    "kind": "binary",
    "versioning": {"mode": "release-please"},
    "assets": {"required": ["app.zip", "app.tar.gz"], "optional": []},
    "registries": {"github": {"required": True}},
}


class PolicyAssetsTest(unittest.TestCase):
    def test_policy_manifest_version_and_hash(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / ".release-policy.yml").write_text(json.dumps(POLICY))
            (root / ".release-please-manifest.json").write_text('{".": "1.2.3"}')
            policy = load_policy(root / ".release-policy.yml")
            self.assertEqual(desired_version(policy, root=root), "1.2.3")
            self.assertEqual(len(policy["_hash"]), 64)

    def test_manual_version_never_accepts_branch_name(self):
        policy = {**POLICY, "versioning": {"mode": "manual", "version": "main"}}
        with self.assertRaises(PolicyError):
            desired_version(policy)

    def test_build_matrix_requires_runner_and_command(self):
        validate_policy({**POLICY, "build": {"matrix": [{"runner": "ubuntu-latest", "command": "make dist"}]}})
        with self.assertRaises(PolicyError):
            validate_policy({**POLICY, "build": {"matrix": [{"runner": "ubuntu-latest"}]}})
        with self.assertRaises(PolicyError):
            validate_policy({**POLICY, "build": {"matrix": [{"runner": "windows-latest", "command": "make", "shell": "cmd"}]}})

    def test_asset_gate_and_checksums(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with zipfile.ZipFile(root / "app.zip", "w") as archive:
                archive.writestr("app", "ok")
            payload = root / "app"
            payload.write_text("ok")
            with tarfile.open(root / "app.tar.gz", "w:gz") as archive:
                archive.add(payload, arcname="app")
            assets = collect_assets(POLICY["assets"]["required"], [], root)
            checksums = write_checksums(assets, root / "SHA256SUMS")
            self.assertEqual(len(checksums.read_text().splitlines()), 2)

    def test_missing_and_broken_assets_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "app.zip").write_text("not a zip")
            with self.assertRaises(AssetError):
                collect_assets(["app.zip"], [], root)
            with self.assertRaises(AssetError):
                collect_assets(["missing"], [], root)

    def test_cleanup_tag_static_gate(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "scripts"
            path.mkdir()
            (path / "prune.sh").write_text("gh release delete v1 --cleanup-tag")
            with self.assertRaises(ReleaseError):
                assert_no_cleanup_tag(directory)


if __name__ == "__main__":
    unittest.main()
