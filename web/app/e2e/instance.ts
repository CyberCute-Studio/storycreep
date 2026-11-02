// An instance of the server for the end-to-end tests: the storycreep
// binary, built with the webdist tag, so that it serves the app's build, as
// the container image does, on a PostgreSQL database of its own, which it
// migrates at startup, and whose first-run setup is pending until something
// completes it. server.ts runs the one that Playwright starts, and a test
// starts its own where it needs another configuration, as one at a LAN
// address, or behind a proxy that terminates TLS (PRD FR-AUTH-02 AC6).
// cmd/e2ehelper makes and drops the databases, and runs the proxy. Node runs
// this file as it is, without a build.

import { type ChildProcess, spawn, spawnSync } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { binaries, binary, databaseURLs, serverModule } from "./environment.ts";

/** Builds the binaries into binaries, anew, or exits the process if it cannot. */
export function build(): void {
  rmSync(binaries, { recursive: true, force: true });
  mkdirSync(binaries, { recursive: true });
  for (const [command, tags] of [
    ["storycreep", ["-tags", "webdist"]],
    ["e2ehelper", []],
  ] as const) {
    const built = spawnSync(
      "go",
      ["build", ...tags, "-o", binary(command), `./cmd/${command}`],
      { cwd: serverModule, stdio: "inherit" },
    );
    if (built.status !== 0) {
      rmSync(binaries, { recursive: true, force: true });
      process.exit(built.status ?? 1);
    }
  }
}

/** Runs e2ehelper with args, and throws if it fails. */
function helper(args: string[]): void {
  const run = spawnSync(binary("e2ehelper"), args, { encoding: "utf8" });
  if (run.status !== 0) {
    throw new Error(
      `e2ehelper ${args[0]}: ${run.stderr || run.error?.message || `status ${run.status}`}`,
    );
  }
}

/** Makes the empty database name, in place of one that a run left. */
export function createDatabase(name: string): void {
  helper(["createdb", "-admin-url", databaseURLs().admin, "-name", name]);
}

/** Drops the database name, with the sessions still connected to it. */
export function dropDatabase(name: string): void {
  helper(["dropdb", "-admin-url", databaseURLs().admin, "-name", name]);
}

/** Returns a TCP port of host that nothing listens on now. */
export async function freePort(host: string): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.unref();
    server.on("error", reject);
    server.listen(0, host, () => {
      const address = server.address();
      server.close(() => {
        if (address === null || typeof address === "string") {
          reject(new Error("no port"));
        } else {
          resolve(address.port);
        }
      });
    });
  });
}

/** The environment of a server of the database name, at address. */
export function serverEnv(options: {
  database: string;
  address: string;
  publicURL: string;
  env?: Record<string, string>;
}): NodeJS.ProcessEnv {
  const urls = databaseURLs(options.database);
  return {
    ...process.env,
    STORYCREEP_HTTP_ADDR: options.address,
    STORYCREEP_PUBLIC_URL: options.publicURL,
    STORYCREEP_ADMIN_ADDR: "off",
    STORYCREEP_LOG_LEVEL: "warn",
    STORYCREEP_DATABASE_URL: urls.app,
    STORYCREEP_DATABASE_ADMIN_URL: urls.admin,
    STORYCREEP_DATABASE_MAINTENANCE_URL: urls.maintenance,
    STORYCREEP_ASSET_STORAGE: join(binaries, `assets-${options.database}`),
    ...options.env,
  };
}

/** Resolves once url answers 200, or rejects once timeout has passed. */
async function ready(url: string, timeout: number): Promise<void> {
  const until = Date.now() + timeout;
  for (;;) {
    try {
      // A request that hangs is given up, so that the deadline is checked.
      const response = await fetch(url, { signal: AbortSignal.timeout(5_000) });
      if (response.ok) {
        return;
      }
    } catch {
      // Not listening yet.
    }
    if (Date.now() > until) {
      throw new Error(`${url} did not answer 200 in ${timeout} ms`);
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

/** Ends child, and resolves once it has exited, or at once if it never ran. */
async function end(child: ChildProcess): Promise<void> {
  if (
    child.pid === undefined ||
    child.exitCode !== null ||
    child.signalCode !== null
  ) {
    return;
  }
  const exited = new Promise<void>((resolve) =>
    child.once("exit", () => resolve()),
  );
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 10_000);
  await exited;
  clearTimeout(timer);
}

/** An instance that a test started. */
export interface Instance {
  /** The instance's public URL, as the browser opens it. */
  url: string;
  /** The setup code that the server logged while setup is pending. */
  setupCode: Promise<string>;
  /** Stops the instance and its proxy, and drops its database. */
  stop(): Promise<void>;
}

export interface InstanceOptions {
  /** The name of the instance's database, which it makes anew. */
  database: string;
  /** The host that the server listens on: a loopback or a LAN address. */
  host: string;
  /**
   * With tls, a proxy listens on a port of 127.0.0.1, terminates TLS with a
   * certificate for localhost, and passes the requests on to the server, on
   * another port of 127.0.0.1, whose public URL is the proxy's.
   */
  tls?: boolean;
  env?: Record<string, string>;
}

/**
 * Starts an instance of the binaries that build built, on a new database,
 * once it is ready. Its first-run setup is pending: the setup code is in its
 * log, which setupCode reads.
 */
export async function startInstance(
  options: InstanceOptions,
): Promise<Instance> {
  createDatabase(options.database);
  const children: ChildProcess[] = [];
  const stop = async () => {
    // The last started first, as the server before its proxy.
    for (const child of [...children].reverse()) {
      await end(child);
    }
    dropDatabase(options.database);
  };
  try {
    const serverPort = await freePort(options.host);
    let url = `http://${options.host}:${serverPort}`;
    if (options.tls) {
      const proxyPort = await freePort("127.0.0.1");
      url = `https://localhost:${proxyPort}`;
      const proxy = spawn(
        binary("e2ehelper"),
        [
          "tlsproxy",
          "-listen",
          `127.0.0.1:${proxyPort}`,
          "-upstream",
          `http://${options.host}:${serverPort}`,
          "-host",
          "localhost",
        ],
        { stdio: ["pipe", "pipe", "inherit"] },
      );
      children.push(proxy);
      // A proxy that never says that it is ready fails the test, which stops
      // it, rather than leaving the test to wait for its own timeout.
      let timer: NodeJS.Timeout | undefined;
      await new Promise<void>((resolve, reject) => {
        timer = setTimeout(
          () => reject(new Error("the TLS proxy was not ready in 30 s")),
          30_000,
        );
        proxy.on("error", reject);
        proxy.once("exit", (code) =>
          reject(new Error(`the TLS proxy exited with ${code}`)),
        );
        createInterface({ input: proxy.stdout as NodeJS.ReadableStream }).on(
          "line",
          (line) => {
            if (line.startsWith("ready ")) {
              resolve();
            }
          },
        );
      }).finally(() => clearTimeout(timer));
    }
    const server = spawn(binary("storycreep"), ["serve"], {
      env: serverEnv({
        database: options.database,
        address: `${options.host}:${serverPort}`,
        publicURL: url,
        env: options.env,
      }),
      stdio: ["ignore", "pipe", "pipe"],
    });
    children.push(server);
    // Rejects if the server cannot be started, or stops before it is
    // stopped, which fails what waits for it, not the test's worker. Its
    // rejection once the instance is stopped is no one's to handle.
    const failed = new Promise<never>((_, reject) => {
      server.on("error", reject);
      server.once("exit", (code, signal) =>
        reject(new Error(`the server exited with ${code ?? signal}`)),
      );
    });
    failed.catch(() => {});
    const logged = new Promise<string>((resolve) => {
      for (const stream of [server.stdout, server.stderr]) {
        createInterface({ input: stream as NodeJS.ReadableStream }).on(
          "line",
          (line) => {
            try {
              const record = JSON.parse(line) as { setup_code?: unknown };
              if (typeof record.setup_code === "string") {
                resolve(record.setup_code);
              }
            } catch {
              // Not a record of the log.
            }
          },
        );
      }
    });
    const setupCode = Promise.race([logged, failed]);
    setupCode.catch(() => {});
    // The server checks its readiness on its own address, which the proxy
    // passes it on to.
    await Promise.race([
      ready(`http://${options.host}:${serverPort}/readyz`, 120_000),
      failed,
    ]);
    return { url, setupCode, stop };
  } catch (error) {
    // What kept the instance from starting is what fails the test, though
    // stopping what it started fails too, which is told beside it.
    await stop().catch((cleanup: unknown) => {
      console.error(
        "stopping the instance that did not start failed:",
        cleanup,
      );
    });
    throw error;
  }
}
