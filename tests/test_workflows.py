import unittest
from pathlib import Path


class WorkflowTest(unittest.TestCase):
    def test_fleet_audit_cannot_trigger_its_own_dashboard_commit(self):
        workflow = Path(".github/workflows/fleet-audit.yml").read_text()
        event_block = workflow.split("permissions:", 1)[0]
        self.assertNotIn("\n  push:", event_block)

    def test_release_steps_use_same_job_numeric_plan_gate(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        self.assertNotIn("needs.plan.outputs.run_release", workflow)
        self.assertGreaterEqual(workflow.count("steps.plan.outputs.run_release == '1'"), 10)


if __name__ == "__main__":
    unittest.main()
