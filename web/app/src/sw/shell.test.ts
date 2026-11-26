import { describe, expect, it } from "vitest";
import paths from "../../../../schema/app-shell/paths.json" with {
  type: "json",
};
import {
  answersWithShell,
  assetsOf,
  newNonce,
  nonceOf,
  shellOf,
  withNonce,
} from "./shell";

// What the service worker does with the app's entry page (PRD FR-OFF-01,
// plan note N95): it answers a navigation with the shell where the server
// would answer it with the entry page, by the paths that the server's tests
// run too (schema/app-shell/paths.json), and gives each answer a nonce of its
// own.

const origin = "https://storycreep.example.com";

describe("answersWithShell", () => {
  it("answers the paths that the server answers with the entry page, and no other", () => {
    for (const path of paths.entry) {
      expect(answersWithShell(new URL(path, origin), origin), path).toBe(true);
    }
    for (const path of paths.other) {
      expect(answersWithShell(new URL(path, origin), origin), path).toBe(false);
    }
  });

  it("answers no path outside the app, nor of another origin", () => {
    for (const path of ["/", "/app", "/login", "/api/v1/instance", "/site/"]) {
      expect(answersWithShell(new URL(path, origin), origin), path).toBe(false);
    }
    expect(
      answersWithShell(
        new URL("/app/projects", "https://other.example.com"),
        origin,
      ),
    ).toBe(false);
  });
});

describe("newNonce", () => {
  it("is 130 random bits as base32, as the server's", () => {
    const nonces = new Set(Array.from({ length: 100 }, () => newNonce()));
    expect(nonces.size).toBe(100);
    for (const nonce of nonces) {
      expect(nonce).toMatch(/^[A-Z2-7]{26}$/);
    }
  });
});

/** The entry page as the server answers it, with nonce. */
function entryPage(nonce: string): { response: Response; body: string } {
  const body = `<!doctype html><meta property="csp-nonce" nonce="${nonce}"><script type="module" src="/app/assets/index-abc.js" nonce="${nonce}"></script>`;
  return {
    body,
    response: new Response(body, {
      status: 200,
      headers: {
        "Content-Type": "text/html; charset=utf-8",
        "Content-Length": String(body.length),
        "Content-Security-Policy": `default-src 'self'; script-src 'self' 'nonce-${nonce}'; style-src 'self' 'nonce-${nonce}'`,
        "X-Robots-Tag": "noindex",
      },
    }),
  };
}

const cachedNonce = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";

describe("withNonce", () => {
  it("gives the cached shell a new nonce, in its policy and in its body, and keeps the rest", async () => {
    const { response, body } = entryPage(cachedNonce);
    const nonce = newNonce();
    const shell = withNonce(response, body, nonce);
    expect(shell.status).toBe(200);
    expect(shell.headers.get("Content-Security-Policy")).toBe(
      `default-src 'self'; script-src 'self' 'nonce-${nonce}'; style-src 'self' 'nonce-${nonce}'`,
    );
    expect(shell.headers.get("X-Robots-Tag")).toBe("noindex");
    expect(shell.headers.get("Content-Type")).toBe("text/html; charset=utf-8");
    // The body is another, whose length the browser counts itself.
    expect(shell.headers.has("Content-Length")).toBe(false);
    const text = await shell.text();
    expect(text).toBe(body.replaceAll(cachedNonce, nonce));
    expect(text).not.toContain(cachedNonce);
    expect(nonceOf(shell)).toBe(nonce);
  });

  it("answers with the body as it is where the policy names no nonce", async () => {
    const body = "<!doctype html><p>placeholder</p>";
    const response = new Response(body, {
      headers: { "Content-Security-Policy": "default-src 'self'" },
    });
    const shell = withNonce(response, body, newNonce());
    expect(await shell.text()).toBe(body);
    expect(shell.headers.get("Content-Security-Policy")).toBe(
      "default-src 'self'",
    );
  });
});

describe("shellOf", () => {
  it("is the same of two answers of one build, whatever their nonces, and not of two builds", () => {
    const a = entryPage(cachedNonce);
    const b = entryPage(newNonce());
    expect(shellOf(a.response, a.body)).toBe(shellOf(b.response, b.body));
    expect(shellOf(a.response, a.body)).not.toContain(cachedNonce);
    const c = entryPage(newNonce());
    const other = c.body.replace("index-abc.js", "index-def.js");
    expect(shellOf(c.response, other)).not.toBe(shellOf(a.response, a.body));
  });
});

describe("assetsOf", () => {
  it("names the files of assets/ that the entry page loads, and no other", () => {
    const shell = `${origin}/app/`;
    const body = [
      '<link rel="icon" type="image/svg+xml" href="/app/favicon.svg" />',
      '<script type="module" crossorigin src="/app/assets/index-abc.js" nonce="N"></script>',
      '<link rel="modulepreload" crossorigin href="assets/chunk-def.js">',
      '<link rel="stylesheet" crossorigin href="/app/assets/index-ghi.css">',
      '<link rel="manifest" href="/app/manifest.webmanifest">',
      '<link rel="preconnect" href="https://other.example.com/app/assets/x.js">',
      '<a href="http://[">',
    ].join("\n");
    expect(assetsOf(body, shell)).toEqual([
      `${origin}/app/assets/index-abc.js`,
      `${origin}/app/assets/chunk-def.js`,
      `${origin}/app/assets/index-ghi.css`,
    ]);
    expect(assetsOf("<!doctype html><p>placeholder</p>", shell)).toEqual([]);
  });
});
