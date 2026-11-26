// Renders the app's icons, which its web app manifest names (PRD FR-OFF-01,
// src/pwa/manifest.ts), into public/icons from public/favicon.svg, in the
// browser that the end-to-end tests run in: the icon at 192 and 512 pixels,
// as it is, and the maskable icon at 512, which a launcher may crop to a
// circle or another shape, as the icon without its rounded corners, whose
// colour fills the square: the letter is within the circle of 80% of the
// width, the part that every shape keeps. Run it after changing the
// favicon, from web/app:
//
//	node scripts/icons.ts
//
// and commit the icons.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const favicon = readFileSync(
  fileURLToPath(new URL("../public/favicon.svg", import.meta.url)),
  "utf8",
);

// The maskable icon fills its square: the favicon without the radius of its
// background's corners.
const corners = ' rx="7"';
if (!favicon.includes(corners)) {
  throw new Error(
    `public/favicon.svg: its background has no${corners}, which the maskable icon takes away; change scripts/icons.ts with it.`,
  );
}
const maskable = favicon.replace(corners, "");

const icons = [
  { file: "icon-192.png", svg: favicon, size: 192 },
  { file: "icon-512.png", svg: favicon, size: 512 },
  { file: "maskable-512.png", svg: maskable, size: 512 },
];

// Playwright has no build of Chromium for Windows on ARM64, so there the
// script runs in Windows's own Edge, as the tests do (playwright.config.ts).
const browser = await chromium.launch({
  channel:
    process.platform === "win32" && process.arch === "arm64"
      ? "msedge"
      : undefined,
});
try {
  for (const { file, svg, size } of icons) {
    const page = await browser.newPage({
      viewport: { width: size, height: size },
      deviceScaleFactor: 1,
    });
    const src = `data:image/svg+xml;base64,${Buffer.from(svg).toString("base64")}`;
    await page.setContent(
      `<style>html, body { margin: 0; background: transparent; }</style><img src="${src}" width="${size}" height="${size}" style="display: block">`,
    );
    await page.locator("img").evaluate((img: HTMLImageElement) => img.decode());
    await page.screenshot({
      path: fileURLToPath(new URL(`../public/icons/${file}`, import.meta.url)),
      omitBackground: true,
    });
    await page.close();
  }
} finally {
  await browser.close();
}
