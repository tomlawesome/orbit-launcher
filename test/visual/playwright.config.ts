import { defineConfig } from "@playwright/test";

// Visual-regression layer of the test pyramid
// (docs/implementation-plan.md section 3.4): renders the real
// orbit-launcher binary through ttyd's built-in xterm.js web UI in a real
// browser, then screenshot-diffs it against a committed baseline.
export default defineConfig({
  testDir: "./tests",
  // beforeAll compiles the real orbit-launcher binary from source (go
  // build), which on a cold Go module cache — a new dependency, or a
  // runner with no prior cache hit — can take well over the previous
  // 15s budget. 60s gives real headroom without masking a genuinely
  // hung build.
  timeout: 60_000,
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
