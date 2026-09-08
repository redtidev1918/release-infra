from __future__ import annotations

import json
import subprocess
from typing import Any


class GitHubError(RuntimeError):
    pass


class GitHub:
    def __init__(self, repo: str | None = None):
        self.repo = repo

    def api(self, endpoint: str, *, method: str = "GET", fields: dict[str, str] | None = None, paginate: bool = False) -> Any:
        command = ["gh", "api", endpoint, "--method", method]
        if paginate:
            command.append("--paginate")
        for key, value in (fields or {}).items():
            command.extend(["-f", f"{key}={value}"])
        result = subprocess.run(command, text=True, capture_output=True)
        if result.returncode:
            raise GitHubError(result.stderr.strip() or result.stdout.strip())
        if not result.stdout.strip():
            return None
        if paginate:
            decoder = json.JSONDecoder()
            content = result.stdout.lstrip()
            values = []
            while content:
                value, offset = decoder.raw_decode(content)
                values.extend(value if isinstance(value, list) else [value])
                content = content[offset:].lstrip()
            return values
        return json.loads(result.stdout)

    def releases(self, repo: str | None = None) -> list[dict]:
        return self.api(f"repos/{repo or self.repo}/releases?per_page=100", paginate=True)

    def tags(self, repo: str | None = None) -> list[dict]:
        return self.api(f"repos/{repo or self.repo}/tags?per_page=100", paginate=True)

    def workflows(self, repo: str | None = None) -> list[dict]:
        return self.api(f"repos/{repo or self.repo}/actions/workflows?per_page=100").get("workflows", [])

    def runs(self, repo: str | None = None) -> list[dict]:
        return self.api(f"repos/{repo or self.repo}/actions/runs?per_page=20").get("workflow_runs", [])
