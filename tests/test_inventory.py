import json
import tempfile
import unittest
from pathlib import Path

from release_infra.inventory import write_outputs


class InventoryTest(unittest.TestCase):
    def test_dashboard_keeps_every_classification(self):
        rows = [
            {"repo": "owner/managed", "visibility": "public", "classification": "managed", "health": "HEALTHY", "actual_assets": ["app"], "latest_release": "v1"},
            {"repo": "owner/fork", "visibility": "public", "classification": "fork", "health": "NO_RELEASE"},
            {"repo": "owner/none", "visibility": "public", "classification": "no-release", "health": "NO_RELEASE"},
        ]
        with tempfile.TemporaryDirectory() as directory:
            write_outputs(rows, directory)
            status = json.loads((Path(directory) / "status.json").read_text())
            self.assertEqual(len(status["repositories"]), 3)
            self.assertIn("owner/fork", (Path(directory) / "STATUS.md").read_text())
            self.assertTrue((Path(directory) / "migration/snapshots/managed.json").exists())


if __name__ == "__main__":
    unittest.main()
