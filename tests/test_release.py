import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from release_infra import release


class ReleaseTest(unittest.TestCase):
    def test_manual_recovery_targets_same_version(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value={"isDraft": True, "assets": []}):
                result = release.plan(str(path), repair=True)
        self.assertEqual(result["version"], "1.2.3")
        self.assertEqual(result["tag"], "v1.2.3")

    def test_retention_deletes_release_objects_without_tags(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "2.0.0"}, "assets": {"required": []}, "registries": {"github": {"required": True}}, "retention": {"stable": 1, "prerelease": 0}}
        rows = [{"tagName": "v2", "isDraft": False, "isPrerelease": False}, {"tagName": "v1", "isDraft": False, "isPrerelease": False}, {"tagName": "v3-rc", "isDraft": False, "isPrerelease": True}]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_run", side_effect=[json.dumps(rows), "", ""]) as run:
                release.prune(str(path))
        commands = [call.args[0] for call in run.call_args_list]
        self.assertIn(["gh", "release", "delete", "v1", "--yes"], commands)
        self.assertNotIn("--cleanup-tag", " ".join(sum(commands, [])))

    def test_stage_dry_run_never_creates_tag_or_release(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / ".release-policy.yml").write_text(json.dumps(policy))
            (root / "dist/release").mkdir(parents=True)
            (root / "dist/release/app").write_text("ok")
            with mock.patch.object(release, "_run", return_value="abc"), mock.patch.object(release, "_remote_tag_commit", return_value=None), mock.patch.object(release, "_release", return_value=None), mock.patch.object(release, "_upload_idempotent") as upload, mock.patch("os.getcwd", return_value=directory):
                previous = Path.cwd()
                try:
                    import os
                    os.chdir(directory)
                    release.stage(dry_run=True)
                finally:
                    os.chdir(previous)
        self.assertTrue(upload.call_args.kwargs["dry_run"])


if __name__ == "__main__":
    unittest.main()
