import unittest

from release_infra.state import ReleaseState, next_action


def release(*, draft=True, assets=()):
    return {"draft": draft, "assets": [{"name": name, "size": 1} for name in assets]}


class StateMachineTest(unittest.TestCase):
    def test_no_change(self):
        state = ReleaseState("1.0.0", "a", "a", release(draft=False, assets=("app",)), ("app",), latest_tag="v1.0.0")
        self.assertEqual(next_action(state), "AUDIT")

    def test_new_desired_version(self):
        self.assertEqual(next_action(ReleaseState("1.1.0", expected_commit="b")), "BUILD")

    def test_missing_historical_tag_is_not_guessed(self):
        self.assertEqual(next_action(ReleaseState("0.9.0", expected_commit="unknown")), "BUILD")

    def test_build_failed_creates_nothing(self):
        self.assertEqual(next_action(ReleaseState("1.1.0")), "BUILD")

    def test_asset_missing(self):
        self.assertEqual(next_action(ReleaseState("1.0.0", "a", "a", release(), ("app",))), "UPLOAD_ASSETS")

    def test_draft_exists(self):
        self.assertEqual(next_action(ReleaseState("1.0.0", "a", "a", release(assets=("app",)), ("app",))), "PUBLISH")

    def test_partial_upload_resumes(self):
        state = ReleaseState("1.0.0", "a", "a", release(assets=("one",)), ("one", "two"))
        self.assertEqual(next_action(state), "UPLOAD_ASSETS")

    def test_registry_already_published(self):
        state = ReleaseState("1.0.0", "a", "a", release(assets=("app",)), ("app",), registries_ok=True)
        self.assertEqual(next_action(state), "PUBLISH")

    def test_registry_hash_mismatch(self):
        state = ReleaseState("1.0.0", "a", "a", release(assets=("app",)), ("app",), registries_ok=False)
        self.assertEqual(next_action(state), "VERIFY_REGISTRIES")

    def test_tag_exists_correct_commit(self):
        self.assertEqual(next_action(ReleaseState("1.0.0", "a", "a")), "CREATE_DRAFT")

    def test_tag_exists_wrong_commit(self):
        self.assertEqual(next_action(ReleaseState("1.0.0", "a", "b")), "FAIL_TAG_CONFLICT")

    def test_public_release_incomplete(self):
        state = ReleaseState("1.0.0", "a", "a", release(draft=False), ("app",))
        self.assertEqual(next_action(state), "FAIL_PUBLIC_INCOMPLETE")

    def test_retry_is_same_draft(self):
        state = ReleaseState("1.0.0", "a", "a", release(assets=("one",)), ("one", "two"))
        self.assertEqual(next_action(state), "UPLOAD_ASSETS")

    def test_latest_wrong(self):
        state = ReleaseState("1.0.0", "a", "a", release(draft=False, assets=("app",)), ("app",), latest_tag="v0.9.0")
        self.assertEqual(next_action(state), "SET_LATEST")

    def test_prerelease_does_not_replace_latest(self):
        state = ReleaseState("2.0.0-rc.1", "a", "a", release(draft=False, assets=("app",)), ("app",), latest_tag="v1.0.0", prerelease=True)
        self.assertEqual(next_action(state), "AUDIT")


if __name__ == "__main__":
    unittest.main()
