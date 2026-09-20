# Security Policy

ai-lb is designed to handle highly sensitive data:

- OAuth access tokens and refresh tokens
- API keys (ai-lb keys and upstream provider keys)
- Session data and cookies
- Account exports and backups
- Provider credentials of any kind

## Never publish sensitive data

NEVER include any of the above in:

- GitHub Issues
- Pull requests, reviews, or comments
- Committed code, config files, logs, or test fixtures
- Screenshots, screen recordings, or pasted terminal output

If you suspect you leaked something: rotate/revoke the credential immediately at the provider, remove it from the report if you still can, and state in the thread that a leak occurred (without re-posting the secret).

## Reporting a vulnerability

There is no private security contact or security email yet.

Do not use public Issues for vulnerability details that could put accounts or tokens at risk. A private reporting channel will be documented here before the first runnable release.

Until then: for anything time-sensitive, open a minimal public issue that describes the area affected without exploit details or credentials, and the maintainer will follow up on how to share details privately.

## Local handling expectations

- Keep real credentials out of the repo: use placeholders and `.env.example`-style files only.
- Local runtime state (databases, token caches, exports) must stay untracked — see `.gitignore`.
- Prefer least privilege when testing against real providers, and use dedicated test accounts where possible.
