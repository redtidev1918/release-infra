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
        self.assertIn("release_infra.cli publish", workflow)
        self.assertLess(workflow.index("release_infra.cli audit"), workflow.index("git tag -f v1"))
        for target in ("linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"):
            self.assertIn(target, workflow)

    def test_readonly_plan_workflow_cannot_dispatch(self):
        workflow = Path(".github/workflows/reusable-readonly-plan.yml").read_text()
        self.assertNotIn("gh workflow run", workflow)
        self.assertNotIn("repository-dispatch", workflow)
        self.assertNotIn("GITHUB_TOKEN:\n        required:", workflow)
        self.assertIn("path: .releasegraph-engine", workflow)
        self.assertIn("./releasegraph fleet", workflow)
        self.assertIn("contents: read", workflow)
        self.assertIn('plan --graph "$GRAPH_PATH" --live', workflow)

    def test_docs_workflow_uses_pinned_actions(self):
        workflow = Path(".github/workflows/docs.yml").read_text()
        for action in re.findall(r"uses:\s*([^\s#]+)@([0-9a-f]+)", workflow):
            self.assertEqual(len(action[1]), 40, action)
        self.assertIn("pages: write", workflow)

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

    def test_finalize_can_reconcile_release_pull_request_labels(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        finalize = workflow.split("  finalize:", 1)[1].split("  release_summary:", 1)[0]
        permissions = finalize.split("steps:", 1)[0]
        self.assertIn("pull-requests: write", permissions)

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


class ProviderReconciliationWorkflowTest(unittest.TestCase):
    """Ordering invariants of the version provider reconciliation layer."""

    def test_ghcr_build_receives_full_release_identity(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        ghcr = workflow.split("Publish GHCR image", 1)[1].split("Required registry verification", 1)[0]
        for arg in ("APP_VERSION=${{ steps.plan.outputs.version }}",
                    "GIT_SHA=${{ github.sha }}",
                    "BUILD_DATE=${{ steps.identity.outputs.build_date }}"):
            self.assertIn(arg, ghcr, f"GHCR build-arg missing: {arg}")
        self.assertIn("org.opencontainers.image.created=", ghcr)
        identity = workflow.index("Stamp release identity")
        self.assertLess(identity, workflow.index("Publish GHCR image"),
                        "the stamped build date must exist before the image build")

    def test_provider_reconcile_runs_before_release_please(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        reconcile = workflow.index("Provider pre-reconcile")
        action = workflow.index(".release-please-action/dist/index.js")
        self.assertLess(reconcile, action, "provider pre-reconcile must precede release-please")
        self.assertIn("provider reconcile --repo", workflow)
        self.assertIn("--apply", workflow)

    def test_release_please_retries_only_transient_github_api_failures(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        release_please = workflow.split("  build-plan:", 1)[0]
        self.assertIn("repository: googleapis/release-please-action", release_please)
        self.assertIn("ref: 45996ed1f6d02564a971a2fa1b5860e934307cf7", release_please)
        self.assertIn("node .release-please-action/dist/index.js", release_please)
        self.assertIn("for attempt in 1 2 3 4", release_please)
        self.assertIn("Something went wrong while executing your query", release_please)
        self.assertIn("API rate limit exceeded", release_please)
        self.assertIn("grep -Eqi", release_please)
        self.assertIn("non-transient or exhausted error", release_please)

    def test_provider_ack_runs_only_after_release_is_published_and_audited(self):
        workflow = Path(".github/workflows/reusable-release.yml").read_text()
        ack = workflow.index("Provider acknowledgement")
        audit = workflow.index("release_infra.cli audit")
        publish = workflow.index("Publish, set Latest, audit, then prune Release objects")
        self.assertLess(publish, ack, "ACK must follow the publish/audit transaction")
        self.assertLess(audit, ack, "ACK must follow the release audit")
        ack_block = workflow[ack:]
        self.assertIn("provider reconcile", ack_block)
        self.assertIn("--apply", ack_block)
        # A non-dry run on a real branch only: never ACK from a pull request.
        self.assertIn("github.event_name != 'pull_request'", ack_block)

    def test_provider_watchdog_never_rewrites_release_history(self):
        workflow = Path(".github/workflows/provider-watchdog.yml").read_text()
        self.assertIn("provider inspect", workflow)
        self.assertIn("provider reconcile", workflow)
        self.assertIn("--apply", workflow)
        for forbidden in ("gh release delete", "gh release edit", "git push --force", "cleanup-tag", "git tag -f"):
            self.assertNotIn(forbidden, workflow)
        # The scheduled run is the one allowed to mutate, and only labels.
        self.assertIn("contents: read", workflow)
        self.assertIn("issues: write", workflow)
        self.assertIn("actions/create-github-app-token@", workflow)
        self.assertIn("RELEASEGRAPH_FLEET_TOKEN: ${{ steps.fleet-token.outputs.token }}", workflow)
        self.assertNotIn("PROFILE_REPO_TOKEN", workflow)

    def test_fleet_rollout_uses_ephemeral_app_token_and_plans_first(self):
        workflow = Path(".github/workflows/fleet-rollout.yml").read_text()
        self.assertIn("actions/create-github-app-token@", workflow)
        self.assertIn("RELEASEGRAPH_FLEET_TOKEN: ${{ steps.fleet-token.outputs.token }}", workflow)
        self.assertIn("permission-contents: write", workflow)
        self.assertIn("permission-pull-requests: write", workflow)
        self.assertIn("permission-workflows: write", workflow)
        self.assertLess(workflow.index("rollout plan"), workflow.index("rollout apply"))
        self.assertIn("if: ${{ inputs.apply }}", workflow)
