import { test, expect, Page } from "@playwright/test";
import { spawn, execFileSync, ChildProcess } from "node:child_process";
import path from "node:path";
import net from "node:net";
import fs from "node:fs";
import os from "node:os";

// #257: the stopped screen after the installer refuses a folder that is
// not empty, at 80x26 -- the case that started the issue. The launcher
// installs where it is started, so ttyd starts it in a folder holding a
// stray file and directory; ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH points it at
// a fixture install.sh that refuses the folder (host event, the
// installer's own lines, exit 1). The folder's path is on screen, so it is
// fixed rather than random.

const repoRoot = path.resolve(__dirname, "../../..");
const fixtureScript = path.resolve(__dirname, "../fixtures/install-not-empty.sh");
const folder = "/tmp/orbit-stopped";

// The development notice's countdown, as test/live's noticeWait: the
// binary keeps its full countdown, so a run through Standard waits it out.
const noticeWaitMs = 22_000;

function findFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, () => {
      const address = srv.address();
      if (address && typeof address === "object") {
        const port = address.port;
        srv.close(() => resolve(port));
      } else {
        srv.close(() => reject(new Error("could not allocate a port")));
      }
    });
  });
}

function buildBinary(): string {
  const binPath = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "orbit-launcher-visual-")), "orbit-launcher");
  execFileSync("go", ["build", "-o", binPath, "./cmd/orbit-launcher"], { cwd: repoRoot, stdio: "inherit" });
  return binPath;
}

// prepareFolder makes the install folder afresh: one stray file and one
// stray directory, which the screen lists.
function prepareFolder(): void {
  fs.rmSync(folder, { recursive: true, force: true });
  fs.mkdirSync(folder, { recursive: true });
  fs.writeFileSync(path.join(folder, "notes.txt"), "left here by hand\n");
  fs.mkdirSync(path.join(folder, "old-site"));
}

let ttyd: ChildProcess;
let port: number;

test.beforeAll(async () => {
  const binPath = buildBinary();
  prepareFolder();
  port = await findFreePort();

  ttyd = spawn("ttyd", ["-p", String(port), "-W", binPath], {
    stdio: "pipe",
    cwd: folder,
    // The same hermeticity switches as splash.spec.ts, for the same
    // reasons: a frozen sky, no update banner, no health probe, no
    // stale-volume pre-flight.
    env: {
      ...process.env,
      ORBIT_LAUNCHER_NO_ANIMATION: "1",
      ORBIT_LAUNCHER_NO_UPDATE_CHECK: "1",
      ORBIT_LAUNCHER_NO_HEALTH_PROBE: "1",
      ORBIT_LAUNCHER_NO_VOLUME_CHECK: "1",
      ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH: fixtureScript,
    },
  });

  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    const up = await new Promise<boolean>((resolve) => {
      const sock = net.createConnection(port, "127.0.0.1");
      sock.once("connect", () => { sock.end(); resolve(true); });
      sock.once("error", () => resolve(false));
    });
    if (up) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`ttyd did not start listening on port ${port} in time`);
});

test.afterAll(() => {
  ttyd?.kill();
  fs.rmSync(folder, { recursive: true, force: true });
});

// screenText is what xterm.js holds on screen, row by row. ttyd exposes
// its terminal as window.term; the canvas has no text to locate.
async function screenText(page: Page): Promise<string> {
  return page.evaluate(() => {
    const term = (window as any).term;
    const buf = term.buffer.active;
    const rows: string[] = [];
    for (let i = 0; i < term.rows; i++) {
      rows.push(buf.getLine(buf.viewportY + i)?.translateToString(true) ?? "");
    }
    return rows.join("\n");
  });
}

async function waitForText(page: Page, text: string, timeout = 10_000): Promise<void> {
  await page.waitForFunction((want) => {
    const term = (window as any).term;
    if (!term) return false;
    const buf = term.buffer.active;
    for (let i = 0; i < term.rows; i++) {
      if ((buf.getLine(buf.viewportY + i)?.translateToString(true) ?? "").includes(want)) return true;
    }
    return false;
  }, text, { timeout });
}

// sizeTerminal resizes the page until ttyd's fitted terminal reports
// cols x rows: the cell size is read off the rendered screen, and the
// viewport grown or shrunk by whole cells plus half a cell of slack.
async function sizeTerminal(page: Page, cols: number, rows: number): Promise<void> {
  await page.waitForFunction(() => Boolean((window as any).term?.cols));
  for (let attempt = 0; attempt < 5; attempt++) {
    const s = await page.evaluate(() => {
      const term = (window as any).term;
      const box = (document.querySelector(".xterm-screen") as HTMLElement).getBoundingClientRect();
      return {
        cols: term.cols as number, rows: term.rows as number,
        cw: box.width / term.cols, ch: box.height / term.rows,
        vw: window.innerWidth, vh: window.innerHeight,
      };
    });
    if (s.cols === cols && s.rows === rows) return;
    // Only a dimension that is off moves: the half cell of slack, added
    // again to one already right, piles up into a whole extra cell.
    await page.setViewportSize({
      width: s.cols === cols ? s.vw : Math.round(s.vw + (cols - s.cols) * s.cw + s.cw / 2),
      height: s.rows === rows ? s.vh : Math.round(s.vh + (rows - s.rows) * s.ch + s.ch / 2),
    });
    await page
      .waitForFunction(([c, r]) => (window as any).term.cols === c && (window as any).term.rows === r, [cols, rows], { timeout: 2000 })
      .catch(() => undefined);
  }
  const got = await page.evaluate(() => [(window as any).term.cols, (window as any).term.rows]);
  throw new Error(`terminal is ${got[0]}x${got[1]}, want ${cols}x${rows}`);
}

test("stopped screen for a folder that is not empty", async ({ page }) => {
  test.setTimeout(120_000);
  await page.goto(`http://127.0.0.1:${port}`);
  await expect(page.locator(".xterm-screen")).toBeVisible();
  await sizeTerminal(page, 80, 26);

  // Install is preselected on a folder with no deployment in it.
  await waitForText(page, "Install");
  await page.keyboard.press("Enter");
  await waitForText(page, "Choose a deployment profile");
  await page.keyboard.press("Enter"); // Standard
  await waitForText(page, "A note before you install");
  await page.keyboard.press("End");
  await page.waitForTimeout(noticeWaitMs);
  await page.keyboard.type("I've read this and I understand");
  await page.keyboard.press("Enter");

  await waitForText(page, "Install stopped", 30_000);
  await waitForText(page, "Install again");
  const text = await screenText(page);
  expect(text).toContain("in the folder: notes.txt, old-site/");
  expect(text).not.toContain("next:");

  await expect(page).toHaveScreenshot("stopped-folder-not-empty.png", { timeout: 10_000 });
});
