import { spawn, ChildProcess } from "node:child_process";
import net from "node:net";

// Helpers shared by the visual specs. Kept small: each spec still decides
// its own folder, binary and extra environment.

export function findFreePort(): Promise<number> {
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

// waitForPort polls until something listens: ttyd has no "ready" signal on
// stdout we can reliably parse across versions.
export async function waitForPort(port: number, timeoutMs = 5000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
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
}

// hermeticEnv turns off everything that would make a screen differ between
// runs or machines: the starfield animation, the update check, the health
// probe, and the Docker volume and running checks.
export function hermeticEnv(): NodeJS.ProcessEnv {
  return {
    ...process.env,
    ORBIT_LAUNCHER_NO_ANIMATION: "1",
    ORBIT_LAUNCHER_NO_UPDATE_CHECK: "1",
    ORBIT_LAUNCHER_NO_HEALTH_PROBE: "1",
    ORBIT_LAUNCHER_NO_VOLUME_CHECK: "1",
    ORBIT_LAUNCHER_NO_RUNNING_CHECK: "1",
  };
}

// startTtyd runs the binary under ttyd in cwd and waits for its port.
export async function startTtyd(
  port: number,
  binPath: string,
  cwd: string,
  extraArgs: string[] = [],
): Promise<ChildProcess> {
  const ttyd = spawn("ttyd", ["-p", String(port), "-W", ...extraArgs, binPath], {
    stdio: "pipe",
    cwd,
    env: hermeticEnv(),
  });
  await waitForPort(port);
  return ttyd;
}
