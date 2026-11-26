/// <reference types="vitest/config" />

import { existsSync } from "node:fs";
import type { ClientRequest, IncomingMessage } from "node:http";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type ProxyOptions } from "vite";
import { VitePWA } from "vite-plugin-pwa";
import { hoverCheck } from "./lint/hover.ts";
import { webManifest } from "./src/pwa/manifest.ts";

/**
 * Where the build goes: the directory that the server's binary embeds when
 * it is built with the webdist tag (server/internal/webassets).
 */
const outDir = fileURLToPath(
  new URL("../../server/internal/webassets/dist/app", import.meta.url),
);

/**
 * What the server replaces, in each response of index.html, with a nonce of
 * that response's Content-Security-Policy (server/internal/httpserver,
 * pages.go). Vite writes it as the nonce of the page's scripts and styles,
 * and of a meta element, from which the app reads it for the styles that it
 * adds itself.
 */
const cspNonce = "__STORYCREEP_CSP_NONCE__";

/**
 * The server that the development server passes the API's requests to, as
 * deploy/README.md runs it, unless STORYCREEP_DEV_API_URL names another. An
 * empty one, as an environment file's template leaves, names none, rather
 * than a URL that every command of Vite, the build and the tests too, would
 * fail on as the configuration loads.
 */
const apiServer = process.env.STORYCREEP_DEV_API_URL || "http://localhost:8080";

/**
 * The server's origin, which a URL with a path, even only a /, as
 * http://localhost:8080/, would not be.
 */
const apiOrigin = new URL(apiServer).origin;

/**
 * The directory of the build where Vite writes the files whose names hold a
 * hash of their content, which the server caches for a year (appAssets, in
 * server/internal/httpserver/pages.go).
 */
const assetsDir = "assets";

// A file of public/ is copied to the build as it is named, with no hash, so
// one in public/assets/ would be cached for a year as if its name changed
// with its content.
if (
  existsSync(fileURLToPath(new URL(`./public/${assetsDir}`, import.meta.url)))
) {
  throw new Error(
    `web/app/public/${assetsDir}/: the server caches the build's ${assetsDir}/ for a year, which only files with a hash in their names may be in; put these files elsewhere in public/.`,
  );
}

// The development server passes these to the server, and gives those from
// its own pages the server's origin, since the server refuses a WebSocket of
// the session cookie from another origin. A request from any other origin
// keeps it, so that the server refuses it as it would without the proxy:
// a page of another port of localhost is of the same site, and its requests
// carry the session cookie.
const proxy: ProxyOptions = {
  target: apiServer,
  changeOrigin: true,
  configure(server) {
    const rewrite = (proxyReq: ClientRequest, req: IncomingMessage) => {
      if (fromDevServer(req)) {
        proxyReq.setHeader("Origin", apiOrigin);
      }
    };
    server.on("proxyReq", rewrite);
    server.on("proxyReqWs", rewrite);
  },
};

/**
 * Reports whether req comes from a page of the development server, whose
 * host its Origin names.
 */
function fromDevServer(req: IncomingMessage): boolean {
  const origin = req.headers.origin;
  if (origin === undefined || req.headers.host === undefined) {
    return false;
  }
  try {
    return new URL(origin).host === req.headers.host;
  } catch {
    return false;
  }
}

export default defineConfig({
  base: "/app/",
  plugins: [
    // The route tree, src/routeTree.gen.ts, generated from src/routes, with
    // each route's component in a chunk of its own.
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
    hoverCheck(),
    // The service worker of src/sw/sw.ts, built into sw.js with the list of
    // every file of the build, which it keeps for the app's shell (PRD
    // FR-OFF-01, plan note N95), and the web app manifest, which index.html
    // links to. The page registers the worker itself (src/pwa/worker.ts),
    // and only in the build: the development server has none.
    VitePWA({
      strategies: "injectManifest",
      srcDir: "src/sw",
      filename: "sw.ts",
      injectRegister: false,
      manifest: webManifest,
      // Every file of the build is in the list once: the plugin adds the
      // manifest itself, and the icons, unless told not to.
      includeManifestIcons: false,
      injectManifest: {
        globPatterns: ["**/*"],
        globIgnores: ["manifest.webmanifest"],
        // The list names the entry page by the path that the server answers
        // with it, /app/, rather than by the file's name, which the server
        // redirects to /app/.
        manifestTransforms: [
          (entries) => ({
            manifest: entries.map((e) =>
              e.url === "index.html" ? { ...e, url: "./" } : e,
            ),
            warnings: [],
          }),
        ],
      },
      devOptions: { enabled: false },
    }),
  ],
  html: { cspNonce },
  // Vite would write a small image or font as a data: URL, which the entry
  // page's Content-Security-Policy refuses: every file is one of the build.
  build: { outDir, assetsDir, emptyOutDir: true, assetsInlineLimit: 0 },
  server: {
    proxy: {
      "/api": { ...proxy, ws: true },
      "/version": proxy,
      "/healthz": proxy,
      "/readyz": proxy,
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}", "lint/**/*.test.ts"],
    setupFiles: ["src/test/setup.ts"],
  },
});
