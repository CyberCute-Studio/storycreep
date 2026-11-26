// The app's web app manifest (PRD FR-OFF-01, plan note N95), which
// vite-plugin-pwa writes as the build's manifest.webmanifest, and links from
// its index.html: the app installs as Storycreep, in a window of its own,
// opens at the tool, /app/, never at /, where the public site may be, and
// is the app of /app/ alone. Its colours are the tokens' light ones
// (@storycreep/shared/tokens.css): the header's, which the window's title
// bar goes with, and the page's, which the splash screen shows; index.html
// names the dark ones too, for the theme of a page in the dark mode, which
// manifest.test.ts holds to the tokens. The icons are in public/icons, which
// scripts/icons.ts renders from public/favicon.svg.

import type { ManifestOptions } from "vite-plugin-pwa";
// The configuration of Vite imports this file, which names its imports by
// their files, extensions too.
import { PRODUCT_NAME } from "../product.ts";

/** Where the server serves the app, which the manifest opens at, and is the app of. */
const appPath = "/app/";

export const webManifest = {
  id: appPath,
  name: PRODUCT_NAME,
  short_name: PRODUCT_NAME,
  start_url: appPath,
  scope: appPath,
  display: "standalone",
  // --color-surface, the header's, and --color-canvas, the page's.
  theme_color: "#f3f4f6",
  background_color: "#ffffff",
  icons: [
    {
      src: "icons/icon-192.png",
      sizes: "192x192",
      type: "image/png",
      purpose: "any",
    },
    {
      src: "icons/icon-512.png",
      sizes: "512x512",
      type: "image/png",
      purpose: "any",
    },
    {
      src: "icons/maskable-512.png",
      sizes: "512x512",
      type: "image/png",
      purpose: "maskable",
    },
  ],
} satisfies Partial<ManifestOptions>;
