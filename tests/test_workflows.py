import unittest
from pathlib import Path


class WorkflowTest(unittest.TestCase):
    def test_fleet_audit_cannot_trigger_its_own_dashboard_commit(self):
        workflow = Path(".github/workflows/fleet-audit.yml").read_text()
        event_block = workflow.split("permissions:", 1)[0]
        self.assertNotIn("\n  push:", event_block)

    def test_release_jobs_only_skip_explicit_noop(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        self.assertEqual(workflow.count("needs.plan.outputs.should_release != 'false'"), 2)


if __name__ == "__main__":
    unittest.main()
