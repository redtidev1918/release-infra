import subprocess
import unittest
from unittest import mock

from release_infra.github import GitHub, GitHubError


class GitHubTest(unittest.TestCase):
    def test_api_retries_transient_gh_failures(self):
        calls = []

        def run(command, **kwargs):
            calls.append(command)
            if len(calls) == 1:
                return subprocess.CompletedProcess(command, 1, "", "connection reset by peer")
            return subprocess.CompletedProcess(command, 0, '{"ok":true}\n', "")

        with mock.patch("release_infra.github.time.sleep"), mock.patch("release_infra.github.subprocess.run", side_effect=run):
            self.assertEqual(GitHub().api("repos/owner/repo"), {"ok": True})
        self.assertEqual(len(calls), 2)

    def test_api_does_not_retry_permission_errors(self):
        with mock.patch("release_infra.github.time.sleep") as sleep, mock.patch(
            "release_infra.github.subprocess.run",
            return_value=subprocess.CompletedProcess([], 1, "", "HTTP 403"),
        ):
            with self.assertRaises(GitHubError):
                GitHub().api("repos/owner/repo")
        sleep.assert_not_called()


if __name__ == "__main__":
    unittest.main()
