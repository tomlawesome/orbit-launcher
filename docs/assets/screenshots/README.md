# Launcher screenshots

Website and documentation captures of the real `orbit-launcher` TUI, built
from `dev` (`82fb05f`) and rendered through ttyd's xterm.js in Chromium, the
same path as `test/visual`. 1280×720 viewport at 2× (2560×1440), DejaVu Sans
Mono 16px, terminal background `#05070d` (`style.Background`).

| File | Screen |
| --- | --- |
| `01-splash.png` | Splash and main menu on a fresh machine (`dormant`) |
| `02-install-profile.png` | Install: choose a deployment profile |
| `03-install-ready.png` | Install: ready to install |
| `04-install-console.png` | Install: mission console mid-run |
| `05-install-success.png` | Install complete: URL, `alive`, Get into Orbit |
| `06-splash-alive.png` | Splash with a healthy deployment and version foot |
| `07-update-confirm.png` | Update: confirm |
| `08-update-console.png` | Update: mission console mid-run |
| `09-repair-proposed.png` | Repair: findings and the proposed safe plan |
| `10-repair-applied.png` | Repair: safe repairs applied |
| `11-remove-confirm.png` | Remove: confirm stand-down |
| `12-remove-done.png` | Remove: stood down, with the full-removal command |
| `arrival-wordmark.png` | Arrival animation, wordmark frame |

Nothing was installed. The engine side was stubbed so every flow could be
shown safely: a local `install.sh` replaying the engine event stream v0
sequence from orbit `dev`'s `scripts/install.sh`, a `repair.sh` speaking the
diagnosis/plan/execute protocol, a no-op `docker` for stand-down, and a
local HTTPS endpoint so `https://orbit.example.com` probes as `alive`. The
launcher binary, its screens and its copy are unmodified. The foot reads
`orbit-launcher dev` because the binary was built without a release version.
