> [!IMPORTANT]
> **Development disclosure:** orbit-launcher was coded by Claude under human
> direction.

<div align="center">

<img src="docs/images/orbit-banner.jpg" alt="Orbit Launcher: the Orbit ring of planets over the sunrise, with LAUNCHER beneath it" width="100%" />

<h3>
  <a href="https://tomlawesome.github.io/orbit-site/">Website</a>
  &nbsp;&nbsp;·&nbsp;&nbsp;
  <a href="https://github.com/tomlawesome/orbit-launcher/security/policy">Report a vulnerability</a>
  &nbsp;&nbsp;·&nbsp;&nbsp;
  <a href="https://github.com/tomlawesome/orbit-launcher/blob/main/LICENSE">Licence</a>
</h3>

<p>
  <strong>orbit-launcher</strong><br />
  The part of Orbit that sets it up and looks after it on your server.
</p>

<br />

<img src="docs/images/launcher-menu.png" alt="The launcher's opening screen: a starfield, the Orbit mark, and the menu: Install, Update, Repair, Remove and Exit" width="92%" />

<br /><br />

<img src="docs/images/launcher-notice.png" alt="Before the first install: a note from Orbit's author, a reading timer, and a phrase to type before it goes ahead" width="45%" />
&nbsp;
<img src="docs/images/launcher-update.png" alt="Updating an existing install: the launcher names the deployment and says what will and will not change before it starts" width="45%" />

</div>

## What it is

orbit-launcher is the full-screen terminal program that installs, updates,
repairs and removes Orbit on the server that hosts it. Orbit's install line
downloads it, checks it and starts it for you.

## What you need

- **A Linux server.** Orbit's download script stops on any other system, and
  on anything but 64-bit x86 or arm64. Orbit itself is built for 64-bit x86
  (amd64) only for now, so use amd64. The launcher is tested by installing
  Orbit on Ubuntu and on Fedora.
- **Docker with Compose v2.** Plus `curl`, `openssl`, `tar`, `sha256sum`,
  `base64` and GNU `timeout`, which most Linux systems already have.
- **A normal user account that can use Docker.** The launcher refuses to run
  as root, with or without `sudo`.
- **Memory.** Standard needs 6 GB: Orbit (2 GB) and its malware scanner
  (4 GB). The Documents profile adds a 1 GB attachment reader, so it needs
  7 GB. The launcher reads the machine's memory on Linux and will not start
  Documents on a smaller machine. It never blocks Standard.
- **Ports.** Orbit listens on port 3000 on every network interface unless
  you change `ORBIT_PORT` or `ORBIT_BIND_ADDRESS` in `.env-orbit`. Put an
  HTTPS reverse proxy in front of it. Never expose Orbit directly to the
  internet. [Installing Orbit](https://github.com/tomlawesome/orbit/blob/main/docs/installing.md)
  covers the proxy.
- **Disk.** No minimum is documented. If Orbit fails to start, the installer
  reports the free space on the install folder and on Docker's data folder.
- **Internet access** to GitHub, to fetch the install files and to check
  whether a newer launcher exists. Set `ORBIT_LAUNCHER_NO_UPDATE_CHECK` to
  any non-empty value to skip the check.

## Install, then claim

The short version. [Installing Orbit](https://github.com/tomlawesome/orbit/blob/main/docs/installing.md)
has the full steps, including what each question means.

1. Make an empty folder for Orbit and open a terminal in it. The launcher
   works on the folder it is started from.
2. Run the install line from Installing Orbit. It checks the signed release
   and starts the launcher.
3. Choose **Install**, then a profile: **Standard** or **Documents**. The
   Agent and Full rows show "not yet".
4. Read the note about Orbit's state and risks. After a 20-second countdown,
   type `I've read this and I understand` to go on.
5. Watch the install run in the launcher's console. If Orbit needs settings
   (its public address, how people sign in), the launcher asks for them in
   its console, or briefly hands you the terminal to answer.
6. On the final screen, the address shows as **alive** and **unclaimed**.
   Choose **Get into Orbit** to open the claim link, or **Copy claim link**
   if the server has no browser. Whoever opens the link first creates the
   first administrator, so open it yourself. If no link shows, run
   `docker compose --env-file .env-orbit logs orbit-app` in the folder and
   open the link Orbit printed.

After that, export a recovery bundle, as Installing Orbit explains.

## Update and Repair

Run the install line again from the Orbit folder and choose:

- **Update** pulls the latest Orbit and updates this deployment, keeping its
  configuration. The launcher takes no backup, and Orbit's note says an upgrade may not
  keep your data, so back up first (see the link at the end). One-step update with a backup, a health check
  and rollback is planned (launcher issue #317, update in one step).
- **Repair** runs the deployment's own `scripts/repair.sh` to diagnose it
  and shows what a fix would do. It changes nothing until you agree, and
  offers only the actions the plan proposes. It cannot yet start services
  that failed to start: launcher issue #256.

## What Remove deletes

Remove works in two steps, and the launcher runs only the first.

1. **Stand down** (the launcher runs this after you confirm). It runs
   `docker compose down`: it stops Orbit and removes its containers and
   network. It deletes no files, no data volumes and no images.
2. **The removal command** (you decide whether to run it). The launcher
   shows a command and offers to copy it, but never runs it. It is
   `docker compose … down -v && sudo rm -rf <your Orbit folder>`. It
   deletes the data volumes (mail, documents) and the whole folder:
   configuration, secrets, backups and anything else you put there. It
   cannot be undone. It leaves Docker images alone.

If the folder is the root folder or your home folder, the launcher offers
only the data-volume half and leaves the folder for you. Removing the data
volumes matters: a database volume left behind makes a later install stop.

Remove acts only on the Orbit found in the folder you start the launcher
in. A change so that Remove deletes only Orbit's own files, and keeps
everything else in the folder, is planned:
launcher issue #285.

## Upgrades and backups

[Upgrades and backups](https://github.com/tomlawesome/orbit/blob/main/docs/upgrades-and-backups.md)
explains how to back up Orbit before an update and how to restore.

## Options

`orbit-launcher --version` prints its version. `orbit-launcher --licences`
prints the licences of what it includes. `ORBIT_LAUNCHER_NO_ANIMATION`, set
to any non-empty value, skips the opening animation.

## For contributors

The launcher is not currently taking outside code changes: see
[CONTRIBUTING.md](CONTRIBUTING.md). Build it with
`go build ./cmd/orbit-launcher` and test it with `go test -race ./...`.
[docs/releasing.md](docs/releasing.md) says how releases are made and
[docs/implementation-plan.md](docs/implementation-plan.md) describes the
code. Orbit builds and signs the launcher with each Orbit release; this
repository holds its source, tests and version tags.
