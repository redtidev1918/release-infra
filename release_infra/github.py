from __future__ import annotations

import json
import subprocess
import time
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
        result = None
        for attempt in range(6):
            result = subprocess.run(command, text=True, capture_output=True)
            if not result.returncode:
                break
            retryable = ("eof", "connection reset", "timed out", "operation timed out", "rate limit", "secondary rate", "http 5")
            if attempt == 3 or not any(text in (result.stderr + result.stdout).lower() for text in retryable):
                raise GitHubError(result.stderr.strip() or result.stdout.strip())
            time.sleep((attempt + 1) * 5)
        assert result is not None
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
