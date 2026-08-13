# orbit-launcher implementation plan

## Purpose

orbit-launcher is a standalone, full-screen terminal application for
installing, updating, repairing and removing an Orbit personal server. It
supersedes the bash-script "command centre" attempted in
[orbit#260](https://github.com/tomlawesome/orbit/issues/260). This document
is the source-of-truth delivery plan: architecture, repository governance,
CI/CD, the full test strategy, and the delivery waves with evidence required
to promote each one.

It deliberately mirrors the structure and discipline of `orbit`'s own
`docs/quality-strategy.md`, `docs/releasing.md` and ADR-0003, adapted to a
single Go binary distributed via GitHub Releases instead of a Node/TypeScript
web app distributed as a container image. Where a mechanism is carried over
unchanged, that's stated explicitly; where it's adapted, the reasoning is
given so it can be argued with.

## Non-goals for this document

This is the delivery plan, not the visual design (`design/mockups.html`
already covers that) and not a decision on `orbit`'s own architecture — the
rest of `orbit` is unaffected and out of scope everywhere below.

---

## 1. Architecture

### 1.1 Two artifacts, two lifecycles

- **The bootstrap script** (`get-orbit-launcher.sh` or similar,
  curl-pipe-bash entry point) has exactly one job: detect OS/arch, download
  the correct prebuilt `orbit-launcher` binary from the latest accepted
  GitHub Release, verify its checksum, and exec into it. Target: under two
  seconds on a normal connection, no Orbit application files touched at all.
- **The `orbit-launcher` binary** (Go, single static binary per OS/arch) is
  everything else: the full-screen TUI, all four/five flows, and — only once
  the user commits at a Final Review screen — the code that actually talks
  to Docker and pulls Orbit's own compose files/images.

This directly answers "I'm not sure we should even pull the files until the
user has selected install" — the bootstrap never fetches Orbit itself, and
neither does the TUI until the explicit commit point in each flow. Selecting
"Install" and clicking through profile/config screens does no network I/O
beyond what's needed to render the screens (e.g. an OIDC discovery check can
happen at the point it's asked for, matching how `orbit`'s existing installer
already defers discovery — see `install.sh`'s
`Provider discovery` step). The splash screen itself is instant: no network
call is on the path to the first frame.

### 1.2 Why Go + Bubble Tea + Lipgloss

Confirmed via the design-mockup phase already agreed with you. Concretely:

- Single static binary per platform — no runtime to install, which matters
  for a *tool that installs a runtime for you*.
- `charmbracelet/bubbletea` gives the full-screen alternate-buffer event
  loop, a `tea.Program` with `Update`/`View`, and a clean tick-based
  animation model — exactly what the starfield and spinner states need.
- `charmbracelet/lipgloss` gives declarative, terminal-safe styling
  (borders, alignment, truecolor-with-fallback) instead of hand-rolled ANSI.
- The same ecosystem (`bubbletea`, `lipgloss`, `bubbles` for reusable
  widgets, `harmonica` for animation easing) is maintained by Charm and used
  in production by comparable tools (`gum`, `glow`, `soft-serve`), so the
  risk of picking a niche framework is low.
- `NO_COLOR` / `TERM=dumb` / non-TTY fallback are first-class concerns in
  `lipgloss` and `bubbletea`, matching the accessibility bar `orbit`'s own
  `installer-ui.sh` already set (ANSI only for a real TTY, plain output
  otherwise) — that bar carries over, not regresses.

### 1.3 Module layout (indicative, refined in Wave 0/1)

```
orbit-launcher/
  cmd/orbit-launcher/       main package, flag parsing, entrypoint
  internal/ui/              bubbletea models: splash, menu, install, update,
                             repair, remove, progress, completion, failure
  internal/ui/starfield/    animation model (pure, unit-testable — see 3.1)
  internal/ui/style/        lipgloss theme: the palette/typography from
                             design/mockups.html, as Go values, single
                             source of truth so the mockup and the app can't
                             drift silently
  internal/deploy/          Docker/Compose orchestration: profile → compose
                             file selection, health probes, stand-down,
                             the "what would this do" dry-run used by tests
  internal/release/         version/revision embedding (ldflags), update
                             self-check against the GitHub Releases API
  scripts/get-orbit-launcher.sh   the bootstrap script (bash, POSIX sh
                             fallback where practical)
  test/                     black-box PTY and visual-regression suites
                             (section 3.3, 3.4) — kept out of internal/ so
                             they can only use the public/binary surface,
                             the same discipline as "Adapter contract" tests
                             in orbit's own quality strategy
```

### 1.4 Talking to Docker

`internal/deploy` shells out to `docker` / `docker compose` (no Docker SDK
dependency, matching `orbit`'s own installer's approach and keeping the
binary's only runtime dependency the Docker CLI the user already has). All
compose file selection and health-probe logic lives here so it's unit- and
integration-testable without a real terminal, leaving `internal/ui` to only
own rendering and input.

---

## 2. Licensing

**Current state:** `orbit` itself has no `LICENSE` file and no `license`
field in `package.json` — there's nothing to copy verbatim. What follows is
a standard dual-license pattern (AGPL-3.0 core, separate commercial license)
used by comparable projects (Grafana, n8n, Chatwoot, Cal.com), scoped to what
I can responsibly set up without legal advice:

- `LICENSE` — unmodified AGPL-3.0 text (FSF canonical).
- `LICENSING.md` — one page: "orbit-launcher is available under AGPL-3.0.
  If those terms don't fit your use (e.g. embedding it in a closed
  product), a commercial license is available — contact `<email>`." No
  invented pricing or terms; that's a business decision, not mine to draft.
- `README.md` license section pointing at both.
- `CONTRIBUTING.md` states plainly that the repository is not currently
  accepting external pull requests, so no CLA is needed — you remain sole
  author, which is what makes the dual-license model sound without any
  extra process. If that changes later, a CLA becomes a real prerequisite
  again at that point, not before.

---

## 3. Testing strategy

Mapped the same way `orbit`'s quality strategy maps requirements to the
cheapest reliable layer, adapted for a terminal binary instead of a web app.
Your message named three tools explicitly — here's where each actually
fits, including where I think a named tool *doesn't* fit and why, plus one
addition (visual regression) that wasn't named but earns its place given how
much this project's value is in *looking* right, not just working right.

| Layer | Tool | Purpose | CI position |
| --- | --- | --- | --- |
| Static | `go vet`, `staticcheck`, `golangci-lint`, `gofmt -l` | catch invalid patterns, unsafe code, style defects | first, parallel |
| Unit | Go `testing` (`go test ./...`) | pure logic: starfield math, profile validation, compose-file selection, version parsing, state-machine transitions | first |
| Component/message-flow | `charmbracelet/x/exp/teatest` | drive a `tea.Model` with synthetic key messages, assert rendered frames, without a real PTY | first, alongside unit |
| Black-box PTY (Go's equivalent of `pexpect`) | `github.com/Netflix/go-expect` + `github.com/creack/pty` | spawn the **real compiled binary** under a real pty, send real keystrokes, assert on real terminal output | before merge to `develop` |
| Visual regression | Playwright + `ttyd`/`gotty` (serves a real pty over a websocket) + `xterm.js` | render the actual TUI in a headless browser exactly as a person would see it, screenshot-diff the splash/menu/progress/completion screens | before merge to `develop`, gated to changes touching `internal/ui` or `internal/ui/style` |
| Compose/deploy integration | Go `testing` + a disposable Docker context | prove profile selection produces a valid, `docker compose config`-verified compose file; prove stand-down actually stops what install started | before preview publication |
| Real virtualized live install | `ubuntu-latest` GitHub Actions job, real Docker, real network | prove an actual `curl \| bash` → Install → healthy Orbit deployment works end to end | preview push (authoritative), release acceptance |

### 3.1 Unit tests

Ordinary Go table tests. The one deliberate design discipline: `internal/ui`
components separate **pure state transition** (testable with plain unit
tests, no terminal) from **rendering** (tested via `teatest`/PTY below).
Mirrors `orbit`'s "state reducers" unit-test category directly.

### 3.2 `teatest` (component tests)

`teatest` runs a real `tea.Program` against an in-memory `io.Writer`/`Reader`
pair — no PTY, no subprocess, fast (milliseconds, not the ~300ms–1s a real
`script`/pty spawn costs, which matters at hundreds of tests). Use it for:
navigating every menu, every keybinding, every escape/cancel path, and
asserting exact rendered byte output for each of the mockup screens once
they're implemented. This is the bulk of the interaction-test suite by
volume.

### 3.3 Black-box PTY tests — Go's equivalent of pexpect

`go-expect` + `creack/pty` gives the same capability pexpect is known for —
spawn a real process under a real pty, expect-match on output, send real
keystrokes — against the **actual compiled binary**, not a mocked model,
while keeping the whole suite in one toolchain (`go test ./...` runs
everything, no separate Python environment to provision). This is where
"does Escape really cancel, does Ctrl-C really restore the terminal" gets
proven against the real thing, the same job the bash `script`-based tests
already do for `orbit`'s existing installer (I have first-hand, very recent
experience with exactly this category of test being subtle and worth
getting right — see the still-open flake investigation on `orbit#281`; the
lesson there transfers directly: prefer waiting on real output/state over a
fixed sleep before sending a signal, and reproduce under real CPU
contention, not just an idle laptop, before trusting a fix). If that
in-flight work lands a clean pattern for this, reuse it here rather than
rediscovering it.

### 3.4 Visual regression — the addition beyond what you asked for

None of the above catches "the banner is two rows too low" or "the starfield
looks garish in a 256-colour terminal" — they check *text and state*, not
*appearance*. Given the entire premise of this rewrite is visual quality,
I think it's worth adding: `ttyd` (or `gotty`) exposes a real pty running
`orbit-launcher` over a websocket; `xterm.js` renders it in a real browser
DOM; Playwright drives that browser, takes a screenshot at defined points
(splash, each menu, progress, completion), and diffs against a committed
baseline image. This is genuinely what Playwright is for — it's just testing
a terminal-shaped web page instead of a web app, which is exactly the
composition `xterm.js`-based terminal sharing tools already use in
production. Gated to only run when `internal/ui`/`internal/ui/style` change,
since it's the slowest layer.

### 3.5 Real virtualized live testing

This is the layer that actually proves "a person could really do this,"
matching `orbit`'s own "Container/operational" and "Representative manual"
layers.

**Target platform, corrected from an earlier draft of this plan**:
orbit-launcher runs *on the Linux server being managed* (SSH in, run it
there — the same way `orbit`'s current installer is used today), not as a
cross-platform desktop tool that reaches out to a remote host. It has no
reason to run on Windows or macOS, because nobody deploys Orbit itself on
those. Scope is Linux only.

**On a distro matrix, corrected from an earlier draft of this
section**: a `ubuntu-latest` → `debian:12` container leg was built to
also cover Debian in CI, matching `orbit`'s own CI-vs-Ubuntu
distro-difference lesson from the #281 investigation. It was dropped
(see issue #59, not wanted going forward): Docker-outside-of-Docker
from inside a nested container hit a structural problem — bind-mounted
volume paths resolve relative to the wrapper container's own
filesystem, which the *host* daemon actually creating the containers
can't see, so `docker compose up` failed before creating anything.
Real Debian coverage doesn't need a second CI leg fighting
container-nesting plumbing to prove: the mechanism was verified
directly on a real Debian host, and via a locally-launched Ubuntu
container on that same host with paths and networking aligned — a
faster, cheaper, more direct answer than automating a second CI leg.
`ubuntu-latest` alone is the CI-automated leg (Docker preinstalled, no
nesting):

- Runs the **actual bootstrap script** end-to-end against the **actual
  latest build artifact** from that CI run (not a stub) —
  `curl`-equivalent, `Install` with a real synthetic profile, wait for
  real health checks, assert the real HTTP endpoint responds, then
  `Remove` and assert the containers are gone.
- Go still cross-compiles Linux `amd64` and `arm64` trivially (arm64
  matters for Raspberry Pi / NAS-class self-hosting, a real segment of
  Orbit's likely audience) — that's a build-target decision (4.2), separate
  from which platforms get live-tested here.
- **Failure-path proof, not just happy-path** (tracked as issue #57,
  not yet built): at least one scenario that kills a dependency
  mid-install (stop Postgres between health probes) and asserts the
  Failure screen's stated reason/action is accurate and the rollback
  claim ("nothing on disk changed") is literally true — same
  discipline as `orbit`'s negative-authorization-matrix and
  recoverable-restore acceptance tests.

This layer runs on every push to the protected `preview` branch
(authoritative, matches `orbit`'s "protected preview push" gate exactly) and
is available on-demand for pull requests via a label
(`run-live-matrix`) to avoid burning it on every commit.

### 3.6 Coverage policy

Same policy as `orbit`'s: collected for diagnostic visibility from Wave 0,
not gated on a global percentage until a measured baseline exists; then
ratchet (never regress from measured baseline), with stronger expectations
on `internal/deploy` (the code that touches a person's real containers and
data) than on `internal/ui` rendering.

---

## 4. Repository governance — the three lanes

Carried over structurally unchanged from `orbit`; branch names, required
checks and the risk-proportional principle match exactly, scoped to what
orbit-launcher actually has (no PostgreSQL/browser-app layer, so those
specific jobs don't apply — the *shape* of "cheap on every PR, expensive
once at the protected release push, verify-only at stable promotion"
does).

### 4.1 Branches

| Branch | Role | Required checks (PR gate) |
| --- | --- | --- |
| `develop` | protected integration branch; ordinary issue branches target this | Static and unit checks · Component tests (`teatest`) · Dependency change and licence policy · CodeQL (go) · CodeQL (actions) |
| `preview` | protected release lane; a reviewed merge from `develop` runs the full pipeline once | same PR-gate checks as `develop`, **plus** the full pipeline below runs as a required push-level job |
| `main` | stable; a PR from `preview` verifies (does not rebuild) that the accepted release artifact matches | Static and unit checks · Verify tested preview for stable merge · Dependency change and licence policy · CodeQL (go) · CodeQL (actions) |
| `hotfix/**` | rare, urgent patch path from `main` | Dependency change and licence policy · CodeQL (go) · CodeQL (actions) |

Same ruleset shape as `orbit`'s four rulesets (`deletion` and
`non_fast_forward` blocked, `required_review_thread_resolution: true`,
merge-commit only). All four rulesets set
`required_approving_review_count: 0`, matching `orbit` — deliberately, not
as a gap: on a single-account repository the account that opens a pull
request cannot approve it, so a non-zero count is unsatisfiable rather
than protective.

**The human gate is explicit owner direction, not a web-UI review.** An
assistant merges a pull request only when the owner has approved that
specific change — conversationally, on the issue, or on the PR; the record
of that direction is the approval. Permission covers only the work it was
given for and is never read as a standing exception for the next PR.

### 4.2 Release pipeline (preview push → main → tag)

Adapted from `orbit`'s digest-identity model to binary-identity:

1. Push to `preview` builds binaries for every target OS/arch with version
   and git revision embedded via `-ldflags`, computes SHA-256 checksums,
   runs the full test pyramid (section 3) including the live matrix, and —
   only if everything passes — publishes a **draft** GitHub Release tagged
   `preview` (mutable pointer, same "tags move, checksums don't" principle
   as `orbit`'s digests) with the binaries, checksums, and a GitHub
   `attest-build-provenance` SLSA attestation attached.
2. A stable PR from `preview` to `main` verifies the `preview` release's
   embedded version/revision matches the PR head and re-verifies the
   provenance attestation — **without rebuilding**, same principle as
   `orbit`'s "Verify tested preview for stable merge" job.
3. Merging that PR and running a manual **Promote tested orbit-launcher
   preview** workflow (approval-gated `production` environment, matching
   `orbit`'s pattern) publishes the exact accepted binaries as a real
   (non-draft) GitHub Release, creates the matching immutable git tag, and
   is the version the bootstrap script's "latest" resolution points at.
4. Automatic semantic versioning per release train, same rule as `orbit`:
   read the highest stable `vMAJOR.MINOR.PATCH` tag, preview trains
   increment minor, `hotfix/*` increments patch, major is a separate human
   decision.

`goreleaser` is the standard tool for step 1 (cross-compilation, checksums,
GitHub Release publishing, and provenance/SBOM hooks) and is what's assumed
in the Wave 0 CI skeleton below rather than hand-rolled build scripts.

### 4.3 CodeQL, dependency review, security features

- **CodeQL**: same workflow shape as `orbit`'s (`push`/`pull_request` on all
  four lane patterns, weekly scheduled scan, pinned action SHAs), matrix
  changed to `go` + `actions` instead of `javascript-typescript` +
  `actions`. `security-extended` query pack, `security-events: write`
  permission scoped to that job only, everything else `contents: read`.
- **Dependency review**: same `actions/dependency-review-action`, same
  `fail-on-severity: high`, same SPDX allow-license list as a starting
  point (Go's ecosystem licensing profile is different from npm's — the
  allow-list will need its own pass in Wave 0 rather than a blind copy,
  but the *mechanism*, thresholds and "no PR comment noise, fail the check
  instead" policy carry over unchanged).
- **Repository security settings**: enable secret scanning, secret scanning
  push protection, and Dependabot security updates — the exact three
  `security_and_analysis` settings confirmed already active on `orbit`.
  Add Dependabot version-update config for Go modules and GitHub Actions
  (orbit doesn't currently have a `dependabot.yml` either — this is a gap
  worth closing in both repos, flagging separately rather than silently
  fixing `orbit`'s).

### 4.4 Planning governance — decided: no attestation machinery (#71)

An earlier draft proposed carrying over `orbit`'s protected-path +
`Planning-Model` attestation mechanism. Since then `orbit` retired that
mechanism entirely (orbit ADR-0011): for a single-owner project where every
change lands through a reviewed pull request under branch protection, the
machinery attested *authorship* rather than verifying *correctness*, and its
maintenance cost exceeded the risk it retired.

**Decision (owner-approved in #71):** orbit-launcher does not carry the
mechanism. The Wave 0 build of it (`tools/checkgovernance`, its CI step,
`.github/planning-governance.json`, and the PR-template attestation and
`Observability-Impact` sections) is removed by the PR recording this
decision; PR review covers operational impact directly. Owner direction per
change (§4.1) is the sole planning gate. If multi-author governance is ever
needed, design it against the situation that exists then rather than
reviving this mechanism.

---

## 5. Milestones, epics and delivery waves

Six waves, each a GitHub Milestone, each with a **promotion gate**: the
exact evidence required before that wave's work is allowed to move from
`develop` to a `preview` push. Every issue within a wave cites which of the
waves' acceptance criteria it satisfies, same traceability discipline as
`orbit`'s requirement-ID citation rule.

### Epics (span waves, tracked as GitHub labels)

- `epic:distribution` — bootstrap script, release pipeline, versioning
- `epic:visual-system` — starfield, theme, layout shell, animation
- `epic:install` — profile → config → review → progress → completion/failure
- `epic:update` / `epic:repair` / `epic:remove`
- `epic:testing-infra` — the harnesses in section 3, not features themselves
- `epic:governance` — CI, branch protection, licensing, security

### Wave 0 — Foundations & governance

**Goal**: an empty, disciplined repository that could pass its own CI on
day one, before a single feature exists.

- Repo scaffold: `go.mod`, module layout (1.3), `Makefile`/`justfile` for
  common commands.
- `LICENSE` (AGPL-3.0), `LICENSING.md`, `CONTRIBUTING.md` with CLA decision
  resolved (2), `SECURITY.md` adapted from `orbit`'s (same disclosure
  process and timelines, scope rewritten for a CLI tool: no household-data
  scope, add "supply-chain/binary tampering" and "credential/secret
  handling during install" to scope).
- Three branches + rulesets (4.1), CodeQL workflow (4.3), dependency-review
  workflow + config (4.3), Dependabot config, PR template with the
  `Observability-Impact` + `Planning-Model` fields (4.4).
- Testing harness scaffolding: `go test ./...` wired into CI, `teatest`
  and `go-expect` dependencies vendored/pinned, the `ttyd`/Playwright
  visual-regression job skeleton (can assert against a placeholder screen
  first — proves the pipeline works before real screens exist).
- Bootstrap script skeleton (`scripts/get-orbit-launcher.sh`): architecture
  detection (amd64/arm64), checksum verification, exec-into-binary, with
  its own small `bats`/shell test suite (mirrors how `orbit`'s
  `install.sh` is tested). Linux only — no OS branch needed.
- `goreleaser` config producing a binary for `linux/amd64` and
  `linux/arm64` — the actual target platform (3.5), not a generic
  "everywhere Go can compile" list.

**Promotion gate**: CI green on a trivial "hello world" TUI binary; a
`preview` push produces a real, downloadable, checksummed GitHub Release;
the bootstrap script successfully downloads and execs that release on a
clean VM.

### Wave 1 — Visual shell

**Goal**: the splash screen and navigation shell you already approved in
`design/mockups.html`, actually running, actually animated.

- `internal/ui/style`: the palette/typography as Go `lipgloss` values,
  single source of truth (consider generating the palette section of
  `design/mockups.html` *from* this Go source in a later wave, so the
  design doc and the app can never silently diverge).
- `internal/ui/starfield`: parallax drift, twinkle, the orbiting dot on the
  ⟡ mark — implemented as a pure, tick-driven model first (unit-testable:
  given N ticks, star positions are deterministic for a fixed seed),
  rendered second.
- Splash screen + 5-item main menu (Install/Update/Repair/Remove/Exit),
  full keyboard navigation, `NO_COLOR`/`TERM=dumb`/non-TTY fallback.
- Visual-regression baseline images committed for the splash screen.

**Promotion gate**: `teatest` suite covers every keybinding and cancel path
on the splash/menu; visual-regression screenshot matches the approved
mockup within an agreed tolerance; live-matrix job launches the real binary
on Linux and confirms the splash renders and responds to input.

### Wave 2 — Install flow

**Goal**: the core value proposition — profile → configuration → final
review → live progress → completion/failure — wired to real Docker.

**Architecture decision, superseding two earlier drafts of this
section**:
`internal/deploy` does not reimplement Docker/Compose orchestration, or
orbit's configuration schema, from scratch. `orbit`'s `install.sh` (and
the `scripts/configure.sh` it calls) already has that logic, hard-won
and tested across many PRs — reimplementing any of it natively in Go
would mean re-earning correctness `orbit` already paid for, and worse,
would tie orbit-launcher's correctness to orbit's exact config schema:
every time `configure.sh`'s required fields changed, orbit-launcher
would need a matching Go code change and a recompiled release just to
keep working. Two things follow from that:

- **No vendoring.** `internal/deploy.FetchInstallScript` downloads the
  current `install.sh` fresh from orbit's stable branch at the moment
  Install or Update is confirmed — not a copy checked into this repo,
  not a pinned revision. `install.sh` itself resolves every other asset
  it needs (compose files, `configure.sh`, `configuration.sh`) from the
  exact source revision recorded in the Docker image's own OCI labels,
  so fetching only this one file is sufficient.
- **No config collection, and no field knowledge at all.** Earlier
  drafts of Install had orbit-launcher collect `APP_URL`/OIDC fields
  itself via Go text inputs and write `.env-orbit` directly, running
  `install.sh` detached from the controlling terminal (`Setsid`) so its
  own guided config never triggered. That shipped, then was corrected
  (issue #51) after review: it's exactly the schema-coupling problem
  above, and it also meant install.sh's own
  `scripts/configure.sh --set-oidc-secret` — which has no
  non-interactive form at all, by design, since it reads a secret from
  a real terminal with echo disabled — could never run. The fix:
  `internal/ui` hands the *real* terminal to `install.sh` for the whole
  run, via bubbletea's `tea.ExecProcess` (the same primitive used to
  wrap `$EDITOR`/`git commit`-style interactive subprocesses — see
  `internal/ui/handoff.go`). `install.sh`'s own
  `prepare_configuration()` already does the right thing end to end
  once it has a controlling terminal: it detects missing fields, runs
  `configure.sh --init` for guided prompts, and collects the secret,
  all natively. orbit-launcher's own screens stop at "which profile"
  and "confirm the handoff" — everything past that is `install.sh`
  running exactly as it would if a person had piped it into `bash`
  themselves, and Update reuses the identical mechanism (see
  `internal/deploy.BuildInstallCommand`).

Screens from `design/mockups.html` sections 03–08, implemented against
this: profile selection (Standard only, for now — AI/Full are honest
"not available yet" stubs); a confirm screen explaining the handoff (no
config-collection screen at all, per the above); completion/failure
based on `install.sh`'s own exit code.

**v5 mission console (issue #73, layered on top without reversing #51).**
The engine run now happens *inside* the TUI first: `internal/deploy.
BuildEngineCommand` stages the same fetched `install.sh` but runs it
`--plain --install|--update`, session-detached (`Setsid`) with stdout as
a pipe, so orbit's engine event stream v0 (orbit `docs/engine-events.md`)
renders natively in `internal/ui/console.go` — framed event log, phase-
keyed stage bar (no percentage; the fill is the engine's own phase
progression), elapsed clock. The detachment is what engages the engine's
documented non-interactive contract: it can never prompt through the
TUI, and with incomplete configuration it refuses before Compose
(`reason=configuration-failure`), rolling the target back via its own
file transaction (verified empirically against both orbit develop and
main). That refusal is precisely where #51's handoff survives: the flow
offers "Continue — guided configuration" and hands the real terminal to
interactive `install.sh` via the same `tea.ExecProcess` mechanism. No
config field enters Go, ever. Outcomes are keyed off events plus exit
codes, never scraped prose; a legacy engine (orbit main today) that
emits no events has its output displayed verbatim and is judged by exit
code alone, with the guided installer offered from the failure screen.
Success concludes on `internal/ui/success.go`: the splash's scene with
the wordmark in alive-green, the deployment URL in the identity slot,
"Orbit achieved in Nm NNs" from the console's real clock, and
Get into Orbit / Terminal / Menu. In-console config prompts stay out of
scope until orbit#297's prompt protocol exists.

**v6 starchart alignment (design/mockups-v6-starchart.html, aligned with
orbit issue #307's web identity; the owner-locked visual law is recorded
durably in design/DECISIONS.md — read it before proposing any visual
change).** Gold (#d8b45a) is the accent —
mark-when-dormant, caret, hero URL, the binary pair's lead (partner in
approach-blue #8fb8ff). The wordmark returned to the letter-spaced
normal-size O R B I T everywhere by owner decision (the v5 half-block
big text is retired; `style.Wordmark` is canonical again). Identity sits
tight under the wordmark; menu items centre individually with the caret
riding left of the selected label; the keybind hint is gone entirely and
each screen's foot is one centred faint line (splash: launcher + orbit
versions; success: the achieved time; console: launcher version). Two
one-shot beats, both colour-ramp/whole-cell honest and skipped by
NO_ANIMATION: the arrival (Get · Into · Orbit fade at centre; the
wordmark takes a 2s gold sweep, slides up, and the menu fades in one
item at a time — any key skips, once per process) and the
restored-orbit drift (on the success screen the gold body eases to a
wider orbit with a brief trail). One test-harness consequence,
discovered by an actual hang: a continuously animating splash never
goes idle, so go-expect's read timeout cannot fire while the arrival
plays — every PTY test sends one benign key first (`skipArrival`),
which the model swallows.

**In-console configuration + repair diagnosis (orbit#297 machine
prompts, orbit#261 slice 1).** When the engine's configuration refusal
lands, the flow now stages a config tree (configure.sh + siblings +
.env-orbit.example fetched from the same channel as install.sh, seeded
with the target's existing configuration), runs `configure.sh --check`
to plan, then drives `--init` and `--set-oidc-secret` with
`ORBIT_CONFIGURE_PROMPTS=machine` — the engine's own prompts, rendered
as in-console input rows (`internal/ui/configcollect.go`), every answer
validated engine-side, rejection classes rendered as honest words, the
secret never echoed. The produced `.env-orbit`/`.orbit-secrets` are
adopted into the target (install.sh's designed "pre-provisioned
configuration shape") and the engine re-runs — this time proceeding.
A legacy configure.sh exits with no protocol line, which is the
capability signal: the flow falls back to the #51 terminal handoff
automatically. Setsid on the configure run is as load-bearing as on the
engine run — a legacy script would otherwise prompt on /dev/tty through
the alt screen. Repair stopped being a stub: the launcher fetches
orbit's standalone `repair.sh` (absence = "diagnosis needs a newer
Orbit", honestly), stages it into the deployment's scripts/ directory,
runs `--plan` (orbit#261 slice 3: the identical read-only diagnosis
plus a classified proposed action per warn/fail finding — still zero
mutation), and renders the plan grammar in honest words: action words,
the resolves class, `backup first` when the contract demands a
checkpoint, and the value-free `manual step:` guidance from stderr.
Plan mode's stdout carries only plan lines plus a `plan result=` line
(verified against the real script); a repair.sh too old for `--plan`
rejects it as a usage error and the flow falls back to `--check`'s
finding/diagnosis rendering — capability by behaviour, as everywhere.
Every plan line is composed to hold inside 80 cells. Execution
(slice 4) is adopted too: when the plan proposes safe actions the menu
offers "Run the safe repairs" — `--execute --safe-only` piped on the
script's documented unattended path (the person's menu choice is the
consent that path expects), rendering `execute`/`execution` lines and
the executor's own full re-diagnosis as the after-picture — and a
planned `rotate-database-credential` offers the guarded rotation,
driven in-console via `ORBIT_REPAIR_PROMPTS=machine` (repair's own
prompt env var) through the typed action word and checkpoint
passphrase prompts, which is the only non-TTY transport the script's
never-automatable contract permits. Esc abandons the session; closed
stdin is the engine's documented zero-mutation abort. The menu never
offers an action the plan didn't propose. Both engine-facing grammars (`prompt*`, `finding`/`diagnosis`)
are parsed in `internal/engine` with the same tolerance rules as the
event stream: unknown trailing fields ignored, unknown enum values
carried verbatim, prose never misparsed.

**Promotion gate**: black-box PTY suite (3.3) covers cancel-at-every-step
up to the handoff; a live-matrix install-to-healthy-endpoint scenario
passes on Linux (still open — no Docker/OIDC available in this working
session, tracked as issue #19); at least one proven failure-path
scenario.

### Wave 3 — Update & Repair

**Goal**: the other two "manage" flows.

- Update: detect an existing deployment, then reuse the exact same
  terminal-handoff mechanism as Install (see Wave 2's architecture
  decision) — `install.sh` is idempotent and its own `configure.sh`
  preserves existing values on rerun, so Update never collects or
  duplicates any config either.
- Repair: can ship as a **non-mutating stub** for v1 if the underlying
  repair engine isn't ready yet — same honest seam `orbit`'s own
  `install.sh` uses today for issue #261 ("Repair remains a non-mutating
  dispatch seam"). Don't claim capability the tool doesn't have.

**Promotion gate**: live-matrix update scenario against a Wave-2-installed
deployment; Repair's stub path is proven non-mutating by test, not just by
inspection.

### Wave 4 — Remove

**Goal**: the flow from this session — confirm, stand down (automated,
reversible), then a copy-pasteable, exact, irreversible removal command
(never auto-executed).

- Screens from `design/mockups.html` sections 09–11.
- Test asserts the app itself **never** invokes the destructive command —
  this is a property worth actively testing (grep the binary's actual
  `docker`/`exec` call sites in a unit test, not just trusting the design
  intent survives implementation).

**Promotion gate**: live-matrix scenario installs, then removes, then
asserts containers/network are gone and the stated file path is exactly
correct; the "app never runs the destructive command" property test passes.

### Wave 5 — Release hardening & v1 promotion

**Goal**: everything needed to point real users at the bootstrap script.

- Wider Linux-distro coverage in the live matrix if warranted (e.g. Fedora,
  Arch — driven by real usage evidence, not speculative support), update
  self-check (orbit-launcher checks its own version against the latest
  release and offers to self-update).
- Documentation: README quickstart, the bootstrap one-liner, a
  `docs/releasing.md` for this repo mirroring `orbit`'s.
- First stable `v1.0.0` promotion through the full pipeline (4.2).

**Promotion gate**: a person outside this session (ideally you) runs the
real one-line bootstrap command on a real machine they own, performs a real
install and a real remove, with zero prior context beyond what's on
screen — the same "representative manual acceptance" bar `orbit` itself
holds release candidates to.

---

## 6. Evidence-for-promotion framework

Every wave's closing PR (the one that merges its last issue into `develop`
and is ready to be included in the next `preview` push) records, in the PR
body, the same categories `orbit` requires per delivery issue:

- which promotion-gate criteria above are satisfied, with links to the CI
  run that proved each one;
- negative/failure paths exercised, not just the happy path;
- the exact `Observability-Impact` declaration (section 4.4);
- for anything touching `internal/deploy` or the bootstrap script: the live
  virtualized test run that proved it against a real Docker host, linked;
- documentation/operator impact, if any screen's copy or behaviour changed
  from what's in `design/mockups.html` — the mockup gets updated in the
  same PR, not left to drift.

This is the literal mechanism by which "record evidence for promotion" gets
enforced, not just stated as a principle.

---

## 7. Immediate next steps

All five decisions from the previous round are resolved: no CLA (repo
closed to outside PRs for now), 1 required approval on every protected
branch (task-scoped pre-authorization only, never standing), `Human` as the
sole `Planning-Model` authority for now, Linux-only target platform (no
macOS/Windows), and `go-expect` alone with no supplementary pexpect suite.

Next: execute Wave 0 against the real (currently empty)
`tomlawesome/orbit-launcher` repository — push the initial scaffold,
configure the three rulesets, enable CodeQL/dependency-review/security
settings, land the bootstrap-script skeleton and CI, and open the Wave 0
GitHub Milestone with its issues filed individually so evidence can be
tracked per-issue rather than only at the wave level.
