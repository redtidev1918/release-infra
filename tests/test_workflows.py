import re
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
        self.assertIn("if: always() && needs.build.result == 'success'", workflow)

    def test_third_party_actions_use_full_pinned_shas(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        for action in re.findall(r"uses:\s*([^\s#]+)@([0-9a-f]+)", workflow):
            self.assertEqual(len(action[1]), 40, action)

    def test_central_release_attaches_metadata(self):
        workflow = Path(".github/workflows/infra-release.yml").read_text()
        self.assertIn("RELEASE-METADATA.json", workflow)
        self.assertIn("gh release create", workflow)

    def test_readonly_plan_workflow_cannot_dispatch(self):
        workflow = Path(".github/workflows/reusable-readonly-plan.yml").read_text()
        self.assertNotIn("gh workflow run", workflow)
        self.assertNotIn("repository-dispatch", workflow)
        self.assertIn("contents: read", workflow)

    def test_release_please_only_manages_version(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        self.assertIn("skip-github-release: true", workflow)

    def test_artifact_transfer_has_bounded_retries(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        self.assertEqual(workflow.count("actions/upload-artifact@"), 4)
        self.assertEqual(workflow.count("actions/download-artifact@"), 4)
        self.assertIn("sleep 60", workflow)

    def test_finalize_configures_git_identity_for_annotated_release_tags(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        finalize = workflow.split("  finalize:", 1)[1]
        self.assertIn('git config user.name "github-actions[bot]"', finalize)
        self.assertIn(
            'git config user.email "41898282+github-actions[bot]@users.noreply.github.com"',
            finalize,
        )

    def test_reusable_workflow_exposes_release_please_component_paths(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        self.assertIn("paths_released:", workflow)
        self.assertIn("jobs.release_please.outputs.paths_released", workflow)
        self.assertIn("steps.rp.outputs.paths_released", workflow)

    def test_finalize_configures_node_registry_before_npm_publication(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        finalize = workflow.split("  finalize:", 1)[1]
        publication = finalize.index("Required registry publication")
        setup = finalize.index("actions/setup-node@")
        self.assertLess(setup, publication)
        self.assertIn("registry-url: https://registry.npmjs.org/", finalize)

    def test_required_registry_verification_retries_registry_propagation(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        finalize = workflow.split("  finalize:", 1)[1]
        verification = finalize.split("Required registry verification", 1)[1].split("      - name:", 1)[0]
        self.assertIn("for attempt in 1 2 3 4 5 6", verification)
        self.assertIn("sleep $((attempt * 10))", verification)

    def test_npm_publish_receives_configured_token(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        finalize = workflow.split("  finalize:", 1)[1]
        publication = finalize.split("Required registry publication", 1)[1].split("      - name:", 1)[0]
        self.assertIn("NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}", publication)

    def test_healthy_public_repair_skips_mutation_and_runs_audit(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        finalize = workflow.split("  finalize:", 1)[1]
        self.assertIn("steps.plan.outputs.release_health != 'healthy'", finalize)
        self.assertIn('release_infra.cli audit --version', finalize)


if __name__ == "__main__":
    unittest.main()
