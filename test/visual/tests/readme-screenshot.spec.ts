import { test, expect } from "@playwright/test";
import { ChildProcess } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { findFreePort, startTtyd } from "../lib/terminal";

// #373: retakes the README's menu picture. Skipped unless
// scripts/readme-screenshot.sh sets ORBIT_README_SCREENSHOT, so `npm test` and
// CI never run it or touch docs/images. The script builds the binary with the
// release flags and passes it (ORBIT_README_BINARY) and the output path
// (ORBIT_README_OUT).
test.skip(!process.env.ORBIT_README_SCREENSHOT, "run it through scripts/readme-screenshot.sh");

// A fixed, empty folder: the screen shows the folder it acts on.
const folder = "/tmp/orbit-readme";

// A 1960x1120 window at scale 1 with a 32px font, clipped to 1940x1120: the
// same 100x30 terminal and 1940x1120 PNG as a 980x560 window at scale 2 with a
// 16px font. Playwright's scale-2 emulation is not used because headless
// Chromium then draws xterm.js at twice the size, so only a quarter of the
// screen lands in the picture.
test.use({ viewport: { width: 1960, height: 1120 }, deviceScaleFactor: 1 });

let ttyd: ChildProcess;
let port: number;

test.beforeAll(async () => {
  const bin = process.env.ORBIT_README_BINARY;
  if (!bin) throw new Error("ORBIT_README_BINARY is not set");
  fs.rmSync(folder, { recursive: true, force: true });
  fs.mkdirSync(folder, { recursive: true });
  port = await findFreePort();
  // 100x30 terminal: fontSize 32 in a 1960x1120 window.
  ttyd = await startTtyd(port, bin, folder, [
    "-t", "fontSize=32",
    "-t", 'theme={"background":"#05070d"}',
  ]);
});

test.afterAll(() => {
  ttyd?.kill();
});

test("retake the README menu picture", async ({ page }) => {
  const out = process.env.ORBIT_README_OUT;
  if (!out) throw new Error("ORBIT_README_OUT is not set");
  await page.goto(`http://127.0.0.1:${port}`);
  await expect(page.locator(".xterm-screen")).toBeVisible();

  // Wait until the screen stops changing: two captures in a row alike.
  const clip = { x: 0, y: 0, width: 1940, height: 1120 };
  let prev = await page.screenshot({ clip });
  const deadline = Date.now() + 15_000;
  for (;;) {
    await page.waitForTimeout(500);
    const next = await page.screenshot({ clip });
    if (next.equals(prev)) {
      fs.mkdirSync(path.dirname(out), { recursive: true });
      fs.writeFileSync(out, next);
      return;
    }
    prev = next;
    if (Date.now() > deadline) throw new Error("the screen never stopped changing");
  }
});
