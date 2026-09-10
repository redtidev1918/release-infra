import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from release_infra import release


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        # plan() probes git for tag drift; keep tests hermetic unless a test
        # overrides these itself (nested patches win).
        self._git_patches = [
            mock.patch.object(release, "_run", return_value="head-commit"),
            mock.patch.object(release, "_remote_tag_commit", return_value=None),
        ]
        for patch in self._git_patches:
            patch.start()
            self.addCleanup(patch.stop)

    def test_manual_recovery_targets_same_version(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value={"isDraft": True, "assets": []}):
                result = release.plan(str(path), repair=True)
        self.assertEqual(result["version"], "1.2.3")
        self.assertEqual(result["tag"], "v1.2.3")
        self.assertEqual(result["run_release"], "1")

    def test_plan_reports_healthy_public_release(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app-*.tgz"]}, "registries": {"github": {"required": True}}}
        release_info = {
            "isDraft": False,
            "assets": [{"name": "app-1.2.3.tgz", "size": 1}, {"name": "SHA256SUMS", "size": 1}, {"name": "RELEASE-METADATA.json", "size": 1}],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=release_info):
                result = release.plan(str(path), repair=True)
        self.assertEqual(result["release_health"], "healthy")
        self.assertEqual(result["run_release"], "1")

    def test_historical_tag_drift_is_not_republished(self):
        policy = {"kind": "container", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": []}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=None), mock.patch.object(release, "_run", return_value="new"), mock.patch.object(release, "_remote_tag_commit", return_value="old"):
                result = release.plan(str(path), repair=True)
        self.assertEqual(result["release_health"], "tag-drift")
        self.assertEqual(result["run_release"], "0")

    def test_force_allows_dry_run_build_validation_for_tag_drift(self):
        policy = {"kind": "container", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": []}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=None), mock.patch.object(release, "_run", return_value="new"), mock.patch.object(release, "_remote_tag_commit", return_value="old"):
                result = release.plan(str(path), force=True)
        self.assertEqual(result["release_health"], "tag-drift")
        self.assertEqual(result["run_release"], "1")

    def test_policy_matrix_is_exported_for_github_actions(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "build": {"matrix": [{"runner": "ubuntu-latest", "command": "make linux"}, {"runner": "windows-latest", "command": "make windows"}]}, "assets": {"required": ["linux", "windows.exe"]}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=None):
                result = release.plan(str(path))
        self.assertEqual(json.loads(result["build_matrix"])["include"][1]["runner"], "windows-latest")

    def test_no_download_assets_skips_checksum_but_keeps_metadata(self):
        policy = {"kind": "container", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": []}, "registries": {"github": {"required": True}, "ghcr": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=None):
                result = release.plan(str(path))
        self.assertEqual(result["has_assets"], "0")
        self.assertEqual(result["checksums_enabled"], "false")

    def test_toolchain_signals_are_exposed(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            (Path(directory) / "go.mod").write_text("module app\n\ngo 1.23\n")
            previous = Path.cwd()
            try:
                import os
                os.chdir(directory)
                with mock.patch.object(release, "_release", return_value=None):
                    result = release.plan(str(path))
            finally:
                os.chdir(previous)
        self.assertEqual(result["needs_go"], "1")
        self.assertEqual(result["go_version"], "1.23")

    def test_nested_flutter_workspace_requests_flutter(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            package = Path(directory) / "packages/app"
            package.mkdir(parents=True)
            (package / "pubspec.yaml").write_text("environment:\n  sdk: flutter\n")
            previous = Path.cwd()
            try:
                import os
                os.chdir(directory)
                with mock.patch.object(release, "_release", return_value=None):
                    result = release.plan(str(path))
            finally:
                os.chdir(previous)
        self.assertEqual(result["needs_flutter"], "1")

    def test_android_project_requests_java(self):
        policy = {"kind": "flutter", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": []}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            (Path(directory) / "android").mkdir()
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            previous = Path.cwd()
            try:
                import os
                os.chdir(directory)
                with mock.patch.object(release, "_release", return_value=None):
                    result = release.plan(str(path))
            finally:
                os.chdir(previous)
        self.assertEqual(result["needs_java"], "1")

    def test_ghcr_channel_is_exposed_without_custom_publish_command(self):
        policy = {"kind": "hybrid", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}, "ghcr": {"required": False, "image": "ghcr.io/owner/app", "platforms": "linux/amd64,linux/arm64"}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=None):
                result = release.plan(str(path))
        self.assertEqual(result["ghcr_enabled"], "true")
        self.assertEqual(result["ghcr_required"], "false")
        self.assertEqual(result["ghcr_image"], "ghcr.io/owner/app")

    def test_incomplete_public_release_waits_for_explicit_repair(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}}}
        public = {"isDraft": False, "assets": [{"name": "app", "size": 1}]}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=public):
                waiting = release.plan(str(path))
                repairing = release.plan(str(path), repair=True)
        self.assertEqual(waiting["run_release"], "0")
        self.assertEqual(waiting["needs_repair"], "true")
        self.assertEqual(repairing["run_release"], "1")

    def test_public_metadata_asset_is_preserved_during_repair(self):
        asset = Path("RELEASE-METADATA.json")
        current = {"isDraft": False, "assets": [{"name": asset.name, "size": 1}]}
        with mock.patch.object(release, "_release", return_value=current), \
                mock.patch.object(release, "_run") as run:
            release._upload_idempotent("v1.2.3", [asset])

        commands = [" ".join(call.args[0]) for call in run.call_args_list]
        self.assertFalse(any("release download" in command for command in commands))
        self.assertFalse(any("release upload" in command for command in commands))

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

    def test_reconcile_release_labels_flips_pending_to_tagged(self):
        with mock.patch.object(
            release, "_run",
            side_effect=["owner/repo", json.dumps([{"number": 30}]), "", ""],
        ) as run:
            release.reconcile_release_labels("2.16.0")
        commands = [call.args[0] for call in run.call_args_list]
        self.assertEqual(commands[0], ["gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"])
        search = commands[1]
        self.assertEqual(search[:4], ["gh", "pr", "list", "--state"])
        self.assertIn('label:"autorelease: pending"', " ".join(search))
        self.assertIn("2.16.0", " ".join(search))
        self.assertEqual(commands[2], ["gh", "pr", "edit", "30", "--remove-label", "autorelease: pending"])
        self.assertEqual(commands[3], ["gh", "pr", "edit", "30", "--add-label", "autorelease: tagged"])

    def test_reconcile_release_labels_swallows_api_failure(self):
        with mock.patch.object(release, "_run", side_effect=release.ReleaseError("boom")):
            release.reconcile_release_labels("2.16.0")  # must not raise

    def test_stage_dry_run_never_creates_tag_or_release(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app"]}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / ".release-policy.yml").write_text(json.dumps(policy))
            (root / "dist/release").mkdir(parents=True)
            (root / "dist/release/app").write_text("ok")
            previous = Path.cwd()
            with mock.patch.object(release, "_run", return_value="abc"), mock.patch.object(release, "_remote_tag_commit") as remote, mock.patch.object(release, "_release"), mock.patch.object(release, "_upload_idempotent") as upload, mock.patch("os.getcwd", return_value=directory):
                try:
                    import os
                    os.chdir(directory)
                    release.stage(dry_run=True)
                finally:
                    os.chdir(previous)
        remote.assert_not_called()
        upload.assert_not_called()


    def test_plan_local_git_failure_is_not_swallowed(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": []}, "registries": {"github": {"required": True}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_release", return_value=None), \
                    mock.patch.object(release, "_run", side_effect=release.ReleaseError("no git")):
                with self.assertRaises(release.ReleaseError):
                    release.plan(str(path))

    def test_retention_prunes_old_drafts_beyond_failed_draft_limit(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "2.0.0"}, "assets": {"required": []}, "registries": {"github": {"required": True}}, "retention": {"stable": 1, "prerelease": 1, "failed_draft": 1}}
        rows = [
            {"tagName": "v-draft-old", "isDraft": True, "isPrerelease": False, "createdAt": "2026-01-01T00:00:00Z"},
            {"tagName": "v1", "isDraft": False, "isPrerelease": False, "publishedAt": "2026-03-01T00:00:00Z"},
            {"tagName": "v-draft-new", "isDraft": True, "isPrerelease": False, "createdAt": "2026-05-01T00:00:00Z"},
        ]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.object(release, "_run", side_effect=[json.dumps(rows), ""]) as run:
                release.prune(str(path))
        deleted = [call.args[0] for call in run.call_args_list if call.args[0][:3] == ["gh", "release", "delete"]]
        self.assertEqual(deleted, [["gh", "release", "delete", "v-draft-old", "--yes"]])

    def test_audit_requires_checksums_to_cover_required_assets(self):
        policy = {"kind": "binary", "versioning": {"mode": "manual", "version": "1.2.3"}, "assets": {"required": ["app-linux", "app-macos"]}, "registries": {"github": {"required": True}}, "checksums": True}
        public = {"isDraft": False, "isPrerelease": False, "isLatest": True, "assets": [{"name": name, "size": 1} for name in ("app-linux", "app-macos", "SHA256SUMS", "RELEASE-METADATA.json")]}
        env = {"GITHUB_SHA": "head-commit"}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".release-policy.yml"
            path.write_text(json.dumps(policy))
            with mock.patch.dict("os.environ", env), \
                    mock.patch.object(release, "_release", return_value=public), \
                    mock.patch.object(release, "_remote_tag_commit", return_value="head-commit"):
                with mock.patch.object(release, "_remote_text", return_value="deadbeef  app-linux\n"):
                    with self.assertRaisesRegex(release.ReleaseError, "app-macos"):
                        release.audit(str(path))
                with mock.patch.object(release, "_remote_text", return_value="a  app-linux\nb  app-macos\n"):
                    release.audit(str(path))


if __name__ == "__main__":
    unittest.main()
