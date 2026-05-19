# Security Policy

## Supported versions

SoyaOS is pre-release (`0.x`). The latest `0.x` minor receives security fixes;
older `0.x` minors do not. Once `1.0.0` ships, this policy will be revised.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

To report a vulnerability:

1. Email **security@soyaos.ai** with the details. PGP key fingerprint will be
   published before `0.1.0` general availability.
2. Or use GitHub's private vulnerability reporting at
   `https://github.com/soyaos/soyaos/security/advisories/new`.

Include:

- A description of the issue and its potential impact
- Steps to reproduce or a proof-of-concept
- The version (or commit SHA) you tested against
- Whether the issue has been disclosed elsewhere

We acknowledge reports within **3 business days** and aim to ship a fix within
**30 days** for confirmed High/Critical vulnerabilities. For complex issues
requiring coordinated disclosure, we will agree on an embargo timeline.

## Embargo

Fixes for embargoed vulnerabilities are developed in the private
`soyaos/security` repository and merged here only after the embargo lifts.
Embargoed disclosures are credited in the changelog with the reporter's
permission.

## Hall of fame

Reporters who responsibly disclose security issues will be acknowledged in
release notes (with their permission) and in the project's hall of fame once
established.
