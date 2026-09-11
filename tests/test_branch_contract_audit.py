import base64
import json
import unittest

from release_infra.github import GitHubError
from release_infra.inventory import _branch_contract_status


class FakeGH:
    def __init__(self, files: dict[str, str]):
        self.files = files

    def api(self, path: str) -> dict:
        name = path.split("/contents/")[-1]
        if name in self.files:
            return {"encoding": "base64", "content": base64.b64encode(self.files[name].encode()).decode()}
        raise GitHubError(f"404: {name}")


SHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"

POLICY = json.dumps(
    {
        "kind": "binary",
        "versioning": {"mode": "manual", "version": "1.0.0"},
        "assets": {"required": []},
        "registries": {},
        "repository": {
            "git": {
                "productionOperations": {
                    "base": "default",
                    "branches": ["chore/cutover-*", "ops/*", "release/*", "hotfix/*"],
                }
            }
        },
    }
)


class BranchContractAuditTest(unittest.TestCase):
    def test_no_policy_no_gate_reports_absent(self):
        status = _branch_contract_status(FakeGH({}), "owner/repo", set(), None)
        self.assertEqual(
            status,
            {"configured": False, "gateInstalled": False, "gatePinned": None, "policyValid": None},
        )

    def test_policy_without_gate(self):
        status = _branch_contract_status(FakeGH({}), "owner/repo", set(), json.loads(POLICY))
        self.assertTrue(status["configured"])
        self.assertFalse(status["gateInstalled"])
        self.assertTrue(status["policyValid"])

    def test_gate_pinned_by_immutable_sha(self):
        caller = (
            "jobs:\n  branch-contract:\n    uses: redtidev1918/releasegraph/"
            f".github/workflows/reusable-branch-contract.yml@{SHA}\n"
        )
        status = _branch_contract_status(
            FakeGH({".github/workflows/branch-contract.yml": caller}),
            "owner/repo",
            {".github/workflows/branch-contract.yml"},
            json.loads(POLICY),
        )
        self.assertTrue(status["gateInstalled"])
        self.assertTrue(status["gatePinned"])

    def test_gate_on_mutable_ref_is_reported_unpinned(self):
        caller = (
            "jobs:\n  branch-contract:\n    uses: redtidev1918/releasegraph/"
            ".github/workflows/reusable-branch-contract.yml@main\n"
        )
        status = _branch_contract_status(
            FakeGH({".github/workflows/branch-contract.yml": caller}),
            "owner/repo",
            {".github/workflows/branch-contract.yml"},
            None,
        )
        self.assertTrue(status["gateInstalled"])
        self.assertFalse(status["gatePinned"])

    def test_invalid_operations_report_policy_invalid(self):
        broken = json.loads(POLICY)
        broken["repository"]["git"]["productionOperations"]["requireLatestBase"] = False
        status = _branch_contract_status(FakeGH({}), "owner/repo", set(), broken)
        self.assertFalse(status["policyValid"])


if __name__ == "__main__":
    unittest.main()
