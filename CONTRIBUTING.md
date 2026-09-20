# Contributing to ai-lb

Thanks for your interest. The project is in early scaffold stage — no implementation yet.

## Current policy

- Documentation fixes are welcome.
- Bug reports and feature discussions are welcome.
- External implementation/code contributions are temporarily not accepted.
- The contributor licensing / relicensing policy will be established before external code contributions are accepted.

## Workflow

For in-scope contributions (docs fixes, reports, discussions):

1. Fork the repo and create a focused branch (`fix/...`, `chore/...`, `docs/...`).
2. Keep commits small and meaningful, with clear messages.
3. Open a PR against the appropriate branch describing the summary, why, and how it was verified.
4. Discuss substantial architectural changes in an issue before writing anything.

## Rules

- NEVER commit or paste secrets: no tokens, API keys, session files, credentials, or account exports — in code, config, logs, screenshots, issues, or PRs.
- Tests/checks: there is no suite yet. They will be added alongside the first implementation; PRs touching future code should include or update them.
- Keep the docs honest: features that are only planned must stay labeled as planned. Do not present direction as shipped behavior.
- Respect the source-available license (PolyForm Noncommercial 1.0.0): no commercial-use contributions without maintainer agreement.

## PR expectations

Fill in `.github/pull_request_template.md`: summary, verification steps, credential impact, and docs updates.
