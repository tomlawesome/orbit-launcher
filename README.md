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

<img src="docs/images/launcher-menu.png" alt="The launcher's opening screen: a starfield, the Orbit mark, and the menu — Install, Update, Repair, Remove" width="92%" />

<br /><br />

<img src="docs/images/launcher-notice.png" alt="Before the first install: a note from Orbit's author, a reading timer, and a phrase to type before it goes ahead" width="45%" />
&nbsp;
<img src="docs/images/launcher-update.png" alt="Updating an existing install: the launcher names the deployment and says what will and will not change before it starts" width="45%" />

</div>

orbit-launcher is the full-screen terminal program that opens when you
install Orbit. It runs on the server that hosts Orbit and gives you a
short menu:

- **Install** sets Orbit up for the first time.
- **Update** brings an existing install up to the latest Orbit release.
- **Repair** checks the install for problems, shows what it would fix,
  and fixes the safe ones when you agree.
- **Remove** stops Orbit, then shows you the command that deletes it and
  its data. You decide whether to run it.

It ships with Orbit and is signed as part of each Orbit release. This
repository holds its source code, tests and version tags.
