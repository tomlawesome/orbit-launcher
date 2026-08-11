# orbit-launcher security policy

orbit-launcher downloads and runs a privileged installer that configures
Docker, writes secrets, and manages a personal server's deployment.
Security reports are taken seriously, and reporters should allow time for
coordinated investigation and remediation.

## Supported versions

orbit-launcher has not yet published a stable v1 release. Until then,
fixes are developed on the active development line and included in
subsequent preview releases. Preview releases are evaluation artifacts,
not supported stable releases.

After v1, the latest stable release receives security fixes. An older
release is supported only when its release notes explicitly designate a
maintenance line.

| Release | Security support |
| --- | --- |
| Active pre-v1 development and versioned preview releases | Fixes are developed and validated here |
| Preview releases | Evaluation only; reports are welcome |
| Latest stable release after v1 | Supported |
| Older commits and superseded releases | Unsupported unless release notes say otherwise |

Run the bootstrap script and binaries from the official release only, and
verify the published checksum before executing anything it downloads.

## Report a vulnerability privately

Use
[GitHub private vulnerability reporting](https://github.com/tomlawesome/orbit-launcher/security/advisories/new).
Do not open a public issue, discussion or pull request containing
vulnerability details before coordinated disclosure.

Include, where available:

- the affected orbit-launcher version or commit;
- relevant environment details with all sensitive values removed;
- clear reproduction steps or a minimal proof of concept;
- the security impact and required attacker access;
- whether the issue has been observed in a real deployment; and
- any suggested mitigation.

Never include credentials, tokens, session material, private keys, or
unredacted logs. Use synthetic data and the private advisory attachment
facility.

## What to expect

- Reports should be acknowledged within three business days.
- An initial assessment should normally follow within seven business days.
- Accepted reports should receive a status update at least every 14 days
  while remediation remains active.
- Fix timing depends on severity, exploitability, affected versions and
  the safety of the remediation.

orbit-launcher does not currently operate a paid bug-bounty programme.

## Scope

Useful reports include:

- checksum/signature verification bypass in the bootstrap script or
  self-update path, or any path that could execute unverified code;
- credential or secret handling during install (staged config, OIDC
  secrets, database passwords) being written insecurely, logged, or left
  behind after a cancelled flow;
- the Remove flow's destructive command being generated incorrectly, or
  the application itself ever executing it directly rather than only
  displaying it for the operator to run;
- privilege escalation via Docker/Compose orchestration;
- supply-chain weaknesses in the release/provenance pipeline; and
- terminal-escape-sequence injection from any rendered value.

For a vulnerability solely in an upstream dependency, report it to that
project first. Also report it privately here when orbit-launcher's use
makes the issue exploitable or requires an orbit-launcher-specific
mitigation.

Ordinary defects, feature requests and non-sensitive hardening suggestions
belong in the public
[issue tracker](https://github.com/tomlawesome/orbit-launcher/issues).

## Responsible research

Good-faith research must:

- use systems and data the reporter owns or has explicit permission to
  test;
- avoid privacy violations, service disruption, destructive actions and
  unnecessary persistence;
- avoid social engineering, denial-of-service traffic, credential attacks
  and automated scanning of systems the reporter does not control; and
- delete retained sensitive test material after the report is resolved.

Maintainers will not pursue action against good-faith research that
follows this policy. This statement does not authorize testing against
third-party services and cannot bind parties other than the
orbit-launcher maintainers.
