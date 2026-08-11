import { defineConfig } from "@playwright/test";

// Visual-regression layer of the test pyramid
// (docs/implementation-plan.md section 3.4): renders the real
// orbit-launcher binary through ttyd's built-in xterm.js web UI in a real
// browser, then screenshot-diffs it against a committed baseline.
export default defineConfig({
  testDir: "./tests",
  timeout: 15_000,
  expect: {
    toHaveScreenshot: { maxDiffPixelRatio: 0.01 },
  },
  fullyParallel: false,
  retries: 0,
  reporter: [["list"]],
  use: {
    trace: "retain-on-failure",
  },
});
