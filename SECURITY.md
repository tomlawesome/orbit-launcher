# orbit-launcher security policy

orbit-launcher runs Orbit's install script on your server. That script sets
up Docker and stores secrets, so security reports are taken seriously.
Please allow time for investigation and a fix.

## Supported versions

orbit-launcher ships inside Orbit's releases. Its own version numbers are
the `vX.Y.Z` source tags in this repository, which Orbit pins and builds.
No launcher 1.0 has been tagged yet, so all versions are still early.

Security fixes go into the development line first and then into the next
tagged version. After a launcher 1.0, the version shipped in the latest
Orbit release is the supported one. An older version is supported only
when its release notes say so.

| Version | Security support |
| --- | --- |
| Development line (`dev`) and tagged versions before launcher 1.0 | Fixes are made and tested here; reports are welcome |
| The launcher in the latest Orbit release after launcher 1.0 | Supported |
| Older commits and superseded versions | Unsupported unless release notes say otherwise |

Install orbit-launcher only through Orbit's installer, which checks the
signature on Orbit's release before it runs anything. This repository
publishes no binaries or checksums of its own.

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

- any way to make the launcher run code that was not checked first;
- the launcher's update check (it reads Orbit's latest release to say
  whether a newer launcher exists) being tricked into showing a false
  notice or reaching somewhere it should not;
- credential or secret handling during install (staged config, OIDC
  secrets, database passwords) being written insecurely, logged, or left
  behind after a cancelled flow;
- the Remove flow's destructive command being generated incorrectly, or
  the application itself ever executing it directly rather than only
  displaying it for the operator to run;
- privilege escalation via Docker/Compose orchestration;
- weaknesses in this repository's CI, its version-tag button or its
  dependency pins; and
- terminal-escape-sequence injection from any rendered value.

Orbit builds, signs and ships the launcher. Problems with that signing,
Orbit's installer or Orbit's release pipeline belong with Orbit: report
them through
[Orbit's private reporting form](https://github.com/tomlawesome/orbit/security/advisories/new).

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
