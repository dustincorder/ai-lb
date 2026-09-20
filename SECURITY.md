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

Do not report security vulnerabilities through public GitHub Issues.

Use GitHub's private vulnerability reporting for this repository via the Security tab / "Report a vulnerability".

## Local handling expectations

- Keep real credentials out of the repo: use placeholders and `.env.example`-style files only.
- Local runtime state (databases, token caches, exports) must stay untracked — see `.gitignore`.
- Prefer least privilege when testing against real providers, and use dedicated test accounts where possible.
