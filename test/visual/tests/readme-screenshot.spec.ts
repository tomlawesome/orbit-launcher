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

// 980x560 CSS pixels at scale 2, clipped to 970x560: a 1940x1120 PNG.
test.use({ viewport: { width: 980, height: 560 }, deviceScaleFactor: 2 });

let ttyd: ChildProcess;
let port: number;

test.beforeAll(async () => {
  const bin = process.env.ORBIT_README_BINARY;
  if (!bin) throw new Error("ORBIT_README_BINARY is not set");
  fs.rmSync(folder, { recursive: true, force: true });
  fs.mkdirSync(folder, { recursive: true });
  port = await findFreePort();
  // 100x30 terminal: fontSize 16 in a 980x560 window.
  ttyd = await startTtyd(port, bin, folder, [
    "-t", "fontSize=16",
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
  const clip = { x: 0, y: 0, width: 970, height: 560 };
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
