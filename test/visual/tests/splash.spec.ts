import { test, expect } from "@playwright/test";
import { spawn, execFileSync, ChildProcess } from "node:child_process";
import path from "node:path";
import net from "node:net";
import fs from "node:fs";
import os from "node:os";

const repoRoot = path.resolve(__dirname, "../../..");

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

let ttyd: ChildProcess;
let port: number;

test.beforeAll(async () => {
  const binPath = buildBinary();
  port = await findFreePort();

  ttyd = spawn("ttyd", ["-p", String(port), "-W", binPath], {
    stdio: "pipe",
    // The starfield otherwise advances every 120ms — a moving background
    // would make every screenshot comparison flaky by construction. See
    // internal/ui.NewSplashModelNoAnimation.
    env: { ...process.env, ORBIT_LAUNCHER_NO_ANIMATION: "1" },
  });

  // ttyd has no "ready" signal on stdout we can reliably parse across
  // versions; poll the port instead of guessing a fixed sleep.
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
