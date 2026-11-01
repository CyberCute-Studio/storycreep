/// <reference types="vitest/config" />

import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type ProxyOptions } from "vite";
import { hoverCheck } from "./lint/hover.ts";

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
 * deploy/README.md runs it.
 */
const apiServer = process.env.STORYCREEP_DEV_API_URL ?? "http://localhost:8080";

// The development server passes these to the server, as its own origin: the
// server refuses a WebSocket of the session cookie from another origin.
const proxy: ProxyOptions = {
  target: apiServer,
  changeOrigin: true,
  configure(server) {
    server.on("proxyReq", (req) => req.setHeader("Origin", apiServer));
    server.on("proxyReqWs", (req) => req.setHeader("Origin", apiServer));
  },
};

export default defineConfig({
  base: "/app/",
  plugins: [
    // The route tree, src/routeTree.gen.ts, generated from src/routes, with
    // each route's component in a chunk of its own.
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
    hoverCheck(),
  ],
  html: { cspNonce },
  build: { outDir, emptyOutDir: true },
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
