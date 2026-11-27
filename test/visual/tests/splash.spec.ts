import { test, expect } from "@playwright/test";
import { execFileSync, ChildProcess } from "node:child_process";
import path from "node:path";
import fs from "node:fs";
import os from "node:os";
import { findFreePort, startTtyd } from "../lib/terminal";

const repoRoot = path.resolve(__dirname, "../../..");

function buildBinary(): string {
  const binPath = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "orbit-launcher-visual-")), "orbit-launcher");
  execFileSync("go", ["build", "-o", binPath, "./cmd/orbit-launcher"], { cwd: repoRoot, stdio: "inherit" });
  return binPath;
}

// folder is where the launcher starts: a fixed, empty folder, because the
// splash shows the folder it acts on (#316) and a path that differs
// between machines would differ in the screenshot. As stopped.spec.ts.
const folder = "/tmp/orbit-splash";

let ttyd: ChildProcess;
let port: number;

test.beforeAll(async () => {
  const binPath = buildBinary();
  fs.rmSync(folder, { recursive: true, force: true });
  fs.mkdirSync(folder, { recursive: true });
  port = await findFreePort();

  // The hermetic environment (animation, update check, health probe, volume
  // and running checks all off) is in lib/terminal.ts, with the reasons.
  ttyd = await startTtyd(port, binPath, folder);
});

test.afterAll(() => {
  ttyd?.kill();
});

test("splash screen renders in a real browser terminal", async ({ page }) => {
  await page.goto(`http://127.0.0.1:${port}`);

  // xterm.js renders to <canvas>, not text DOM — there's no reliable text
  // locator to assert against. Wait for the terminal surface to mount,
  // then let toHaveScreenshot's own internal poll-until-stable retry
  // absorb the remaining render/websocket latency rather than guessing a
  // fixed sleep (the same lesson from the orbit#281 signal-timing
  // investigation: wait on real state, not a clock).
  await expect(page.locator(".xterm-screen")).toBeVisible();

  await expect(page).toHaveScreenshot("splash.png", { timeout: 10_000 });
});
