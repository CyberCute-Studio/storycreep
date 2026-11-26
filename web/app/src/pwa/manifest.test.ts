import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { webManifest } from "./manifest";

// The app's web app manifest (PRD FR-OFF-01, plan note N95), which
// vite-plugin-pwa writes into the build, and its icons: what Chromium needs
// of an app to install it, which the end-to-end tests check in the browser
// (e2e/pwa.spec.ts), and colours that are the tokens'.

/** Returns the file at path, from web/app. */
function read(path: string): Buffer {
  return readFileSync(join(import.meta.dirname, "../..", path));
}

/** Returns the light and the dark colour of the token name of tokens.css. */
function token(name: string): { light: string; dark: string } {
  const css = readFileSync(
    join(import.meta.dirname, "../../../shared/src/tokens/tokens.css"),
    "utf8",
  );
  const match = css.match(
    new RegExp(
      `--color-${name}: light-dark\\((#[0-9a-f]{6}), (#[0-9a-f]{6})\\);`,
    ),
  );
  if (match === null) {
    throw new Error(`tokens.css has no --color-${name}`);
  }
  return { light: match[1], dark: match[2] };
}

/** Returns the width and the height of the PNG data, from its header. */
function pngSize(data: Buffer): { width: number; height: number } {
  expect(data.subarray(1, 4).toString("latin1")).toBe("PNG");
  return { width: data.readUInt32BE(16), height: data.readUInt32BE(20) };
}

describe("the web app manifest", () => {
  // Verifies: FR-OFF-01/AC1
  it("opens the installed app at the tool, in a window of its own, as the app of /app/ alone", () => {
    expect(webManifest.start_url).toBe("/app/");
    expect(webManifest.scope).toBe("/app/");
    expect(webManifest.id).toBe("/app/");
    expect(webManifest.display).toBe("standalone");
    expect(webManifest.name).toBe("Storycreep");
    expect(webManifest.short_name).toBe("Storycreep");
  });

  // Verifies: FR-OFF-01/AC3
  it("names icons of 192 and 512 pixels, and a maskable one, which are PNGs of those sizes", () => {
    const sizes = webManifest.icons.map((icon) => ({
      purpose: icon.purpose,
      sizes: icon.sizes,
    }));
    expect(sizes).toEqual([
      { purpose: "any", sizes: "192x192" },
      { purpose: "any", sizes: "512x512" },
      { purpose: "maskable", sizes: "512x512" },
    ]);
    for (const icon of webManifest.icons) {
      expect(icon.type).toBe("image/png");
      const [width, height] = icon.sizes.split("x").map(Number);
      expect(pngSize(read(`public/${icon.src}`)), icon.src).toEqual({
        width,
        height,
      });
    }
  });

  it("has the colours of the header and of the page, which index.html names for both modes", () => {
    const surface = token("surface");
    expect(webManifest.theme_color).toBe(surface.light);
    expect(webManifest.background_color).toBe(token("canvas").light);
    const html = read("index.html").toString("utf8");
    expect(html).toContain(
      `<meta name="theme-color" media="(prefers-color-scheme: light)" content="${surface.light}" />`,
    );
    expect(html).toContain(
      `<meta name="theme-color" media="(prefers-color-scheme: dark)" content="${surface.dark}" />`,
    );
  });
});
