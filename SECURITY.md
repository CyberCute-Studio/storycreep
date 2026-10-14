# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: open the repository's **Security** tab and choose **Report a vulnerability**, or go directly to <https://github.com/CyberCute-Studio/storycreep/security/advisories/new>. Do not open a public issue, pull request, or discussion for a suspected vulnerability.

A useful report says:

- which component is affected (server, web app, public site, Godot addon, container image, or published file format), and which version or commit;
- how to reproduce the issue, ideally with a minimal example;
- what an attacker gains, and under which deployment (self-hosted or hosted, HTTP or HTTPS).

## What happens next

- We aim to acknowledge the report within 5 working days.
- We confirm or rule out the issue, agree a disclosure date with you, and keep you informed until the fix is released.
- We credit you in the advisory unless you ask us not to.

## Disclosure

We follow a 90-day coordinated disclosure policy. We publish the advisory when a fixed release is available, or 90 days after the report, whichever comes first. The date can move by agreement with the reporter.

Security releases are announced in the changelog (`/changelog`) and, from v1, in the release feed.

## Supported versions

Storycreep has not had a release yet. Until the first release, only the `main` branch is supported. After that, security fixes go into the latest release.

## Dependencies

CI checks the Go and JavaScript dependencies for known vulnerabilities on every pull request and every week (`govulncheck` and `pnpm audit`). A high- or critical-severity vulnerability in a dependency is patched within 7 days of being found.
