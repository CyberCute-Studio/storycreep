// What the end-to-end tests, Playwright's configuration, and the server
// that it starts (server.ts) share: the port of that server, where its
// binaries are built, the PostgreSQL server that their databases are on,
// and the first owner, whom unattended first-run setup creates. Node runs
// this file as it is, without a build, as it runs server.ts.

import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

/** The port of the server that Playwright starts. */
export const port = Number(process.env.STORYCREEP_E2E_PORT ?? 8788);

/** The URL of the server that Playwright starts. */
export const baseURL = `http://127.0.0.1:${port}`;

/**
 * The database of the server that Playwright starts, which server.ts makes
 * anew for the run, and which the peer (peer.ts) shares.
 */
export const mainDatabase = `storycreep_e2e_${port}`;

/**
 * The port of the peer: a second process of the same instance, on the
 * server's database, which Playwright starts once the server is ready
 * (peer.ts), by default on the port after the server's: a run takes two
 * ports, so another run keeps apart from it on a port at least two away,
 * as one on the port next to it would share one, which Playwright refuses
 * to start, as it finds the port's URL answering already.
 */
export const peerPort = Number(
  process.env.STORYCREEP_E2E_PEER_PORT ?? port + 1,
);

/** The URL of the peer, its public URL, as the browser opens it. */
export const peerURL = `http://127.0.0.1:${peerPort}`;

/** The server's Go module. */
export const serverModule = fileURLToPath(
  new URL("../../../server/", import.meta.url),
);

/**
 * The directory of the binaries and of the uploaded files: one for each port
 * that the server listens on, so that runs on other ports keep apart.
 */
export const binaries = join(tmpdir(), `storycreep-e2e-${port}`);

/** Returns the path of the binary of command, as built into binaries. */
export function binary(
  command: "storycreep" | "e2ehelper" | "fixturegen",
): string {
  return join(
    binaries,
    process.platform === "win32" ? `${command}.exe` : command,
  );
}

/** The three logins of a database of the instance (STORYCREEP_DATABASE_*). */
export interface DatabaseURLs {
  app: string;
  admin: string;
  maintenance: string;
}

/**
 * Returns the logins of the database name, on the PostgreSQL server of the
 * development compose file, whose password and port its variables set, as
 * they do the file's, if the environment has them (deploy/dev), or the one
 * that the STORYCREEP_E2E_DATABASE_* variables name, as CI's does. Each run
 * has a database of its own, which it makes, and drops (server.ts,
 * instance.ts), through the logins of the database that they name, which
 * must exist: the server's own, without name.
 */
export function databaseURLs(name?: string): DatabaseURLs {
  const password = encodeURIComponent(
    process.env.STORYCREEP_DEV_DB_PASSWORD || "storycreep-dev",
  );
  const dbPort = process.env.STORYCREEP_DEV_DB_PORT || "5432";
  const dev = (login: string) =>
    `postgres://${login}:${password}@localhost:${dbPort}/storycreep`;
  const named = (url: string) => {
    if (name === undefined) {
      return url;
    }
    const u = new URL(url);
    u.pathname = `/${name}`;
    return u.href;
  };
  return {
    app: named(process.env.STORYCREEP_E2E_DATABASE_URL ?? dev("storycreep")),
    admin: named(
      process.env.STORYCREEP_E2E_DATABASE_ADMIN_URL ?? dev("postgres"),
    ),
    maintenance: named(
      process.env.STORYCREEP_E2E_DATABASE_MAINTENANCE_URL ??
        dev("storycreep_maintainer"),
    ),
  };
}

/**
 * The first owner of the server that Playwright starts, an instance admin,
 * whom unattended first-run setup creates, and whom the tests sign in as to
 * take links that an instance admin gives out (PRD FR-AUTH-03).
 */
export const owner = {
  email: "owner@example.com",
  name: "Olive Owner",
  password: "e2e owner passphrase of item 54",
  workspace: "Owner's studio",
} as const;
