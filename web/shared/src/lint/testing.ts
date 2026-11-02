// A harness for the tests of the repository's Biome plugins, the lint rules
// of the web's source: it lints fixtures as if they were files of the
// repository, in a copy of its biome.json and of every plugin that it names,
// so that a test checks the configuration that CI runs, not a copy of it.

import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/** The repository's root. */
export const repositoryRoot = fileURLToPath(
  new URL("../../../../", import.meta.url),
);

/** A diagnostic of Biome's JSON reporter. */
export interface Diagnostic {
  category: string;
  message: string;
  location: { path: string; start: { line: number } };
}

/** The plugins that the repository's biome.json names, from its root. */
export function configuredPlugins(): string[] {
  const config = JSON.parse(
    readFileSync(join(repositoryRoot, "biome.json"), "utf8"),
  ) as { plugins?: string[]; overrides?: { plugins?: string[] }[] };
  const plugins = [
    ...(config.plugins ?? []),
    ...(config.overrides ?? []).flatMap((o) => o.plugins ?? []),
  ];
  return [...new Set(plugins.map((p) => p.replace(/^\.\//, "")))];
}

/**
 * Lints fixtures as files of the repository, and returns Biome's
 * diagnostics: placed maps a path, from the repository's root, to the
 * fixture, from the root too, that is linted there.
 */
export function lintPlaced(placed: Record<string, string>): Diagnostic[] {
  const dir = mkdtempSync(join(tmpdir(), "storycreep-lint-"));
  try {
    const copy = (from: string, to: string) => {
      mkdirSync(dirname(join(dir, to)), { recursive: true });
      copyFileSync(join(repositoryRoot, from), join(dir, to));
    };
    copy("biome.json", "biome.json");
    for (const plugin of configuredPlugins()) {
      copy(plugin, plugin);
    }
    for (const [to, fixture] of Object.entries(placed)) {
      copy(fixture, to);
    }
    const biome = join(
      repositoryRoot,
      "node_modules",
      "@biomejs",
      "biome",
      "bin",
      "biome",
    );
    // The copy is not a git checkout, so Biome is told not to look for one.
    const run = spawnSync(
      process.execPath,
      [biome, "lint", "--vcs-enabled=false", "--reporter=json", "."],
      { cwd: dir, encoding: "utf8" },
    );
    if (run.error) {
      throw run.error;
    }
    return (JSON.parse(run.stdout) as { diagnostics: Diagnostic[] })
      .diagnostics;
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

/**
 * Returns the lines of the plugins' diagnostics in the file at path, from the
 * repository's root, in order, and those whose message has text if it is
 * given, which tells one plugin's from another's.
 */
export function reportedLines(
  diagnostics: readonly Diagnostic[],
  path: string,
  text?: string,
): number[] {
  return diagnostics
    .filter(
      (d) =>
        d.category === "plugin" &&
        d.location.path.replaceAll("\\", "/") === path &&
        (text === undefined || d.message.includes(text)),
    )
    .map((d) => d.location.start.line)
    .sort((a, b) => a - b);
}
