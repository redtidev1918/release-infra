# Security Policy

## Supported versions

The stable `v1` channel receives security fixes. Consumers should pin reusable workflows to a full SHA or the maintained `v1` major channel; do not use `@main` for production releases.

## Reporting

Privately report credential exposure, release integrity, or permission-escalation issues through GitHub's private vulnerability reporting. Do not publish secrets, proof-of-concept tokens, or production repository names in a public issue.

## Credentials

- Audit-only use requires no write permission.
- Release management uses the repository's short-lived `GITHUB_TOKEN`.
- Cross-repository dispatch prefers a GitHub App installation token issued inside an ephemeral Action.
- Fine-grained PATs are a fallback; classic all-repository PATs are not recommended.
- Never place tokens, signing keys, or keystore contents in policies or release metadata.
