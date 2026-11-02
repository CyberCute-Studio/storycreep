// The check that no interaction depends on hover (PRD FR-EDIT-09 AC2): a
// touch screen has no hover, so a control that only a :hover rule shows, or
// lets the pointer reach, cannot be used there. The check reads the app's
// built CSS, Tailwind's utilities and every other rule alike, and refuses a
// rule that applies on hover, whose selector mentions :hover anywhere,
// within :is(), :where(), :not(), or :has() too, or that is nested in such a
// rule, or in a @scope whose bounds mention it, unless every declaration of
// it sets one of the properties of hoverProperties, which change how an
// element looks, never whether it is shown or where it is. So hover may
// colour a control, underline it, or shadow it, but not reveal it, move it,
// or make it larger, which would leave a touch screen's user without it. It
// refuses, too, CSS that lightningcss cannot read, as a selector or an
// at-rule newer than it knows, which it would otherwise skip, with any rule
// there that applies on hover, which a browser may still apply. hoverCheck
// runs the check when Vite builds the app, and fails the build on what it
// refuses. It reads every stylesheet that the build has: those that Vite
// writes, the <style> elements of the build's pages, as index.html's, and
// the stylesheets of public/, which Vite copies to the build as they are.
// CSS that a script imports as a string, with ?inline or ?raw, goes into the
// script, where the check cannot read it, so the build refuses it too.
//
// The check cannot tell a colour that tints a control from one that makes it
// visible, as from transparent, so a control is never transparent, nor the
// colour of what is behind it, until hover: that is for review to catch, as
// CSS that a script writes itself is.

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import {
  type DeclarationBlock,
  type Rule,
  type StyleRule,
  transform,
} from "lightningcss";
import type { Plugin } from "vite";

/**
 * The properties that a rule that applies on hover may set: colours, a
 * text's decoration, a shadow, and the pointer's cursor. Every other
 * property is refused, since it may show, hide, move, or resize what it
 * styles: display, opacity, a background's image, which may be an icon, or a
 * custom property, which may feed any of them, as Tailwind's shadow and ring
 * utilities set. A property joins the list only if it cannot do that.
 */
export const hoverProperties: ReadonlySet<string> = new Set([
  "color",
  "background-color",
  "border-color",
  "border-top-color",
  "border-right-color",
  "border-bottom-color",
  "border-left-color",
  "border-block-color",
  "border-block-start-color",
  "border-block-end-color",
  "border-inline-color",
  "border-inline-start-color",
  "border-inline-end-color",
  "outline-color",
  "text-decoration",
  "text-decoration-line",
  "text-decoration-color",
  "text-decoration-style",
  "text-decoration-thickness",
  "text-underline-offset",
  "box-shadow",
  "cursor",
  "fill",
  "stroke",
]);

/**
 * A declaration that a rule that applies on hover may not make, or CSS that
 * the check could not read.
 */
export interface HoverProblem {
  file: string;
  /** The line of the rule, from 1. */
  line: number;
  /** The column of the rule, from 1. */
  column: number;
  /** The rule's selector, as the source writes it, or "" for CSS that the check could not read. */
  selector: string;
  /** The property that the rule sets, or "" for CSS that the check could not read. */
  property: string;
  /** What lightningcss could not read there, for CSS that the check could not read. */
  unread?: string;
}

/** Returns the declarations of the stylesheet css that hover may not make. */
export function checkHover(css: string, file: string): HoverProblem[] {
  const problems: HoverProblem[] = [];
  const lines = css.split("\n");
  // A rule's selector, as the source writes it: from where the rule starts
  // to its block's brace.
  const selectorAt = (line: number, column: number): string => {
    const rest = (lines[line] ?? "").slice(column - 1);
    const brace = rest.indexOf("{");
    return (brace < 0 ? rest : rest.slice(0, brace)).trim();
  };
  // Reports each declaration of block, of the rule at loc, which applies on
  // hover, that sets a property that hoverProperties does not list.
  const check = (
    block: DeclarationBlock | undefined,
    loc: { line: number; column: number },
  ): void => {
    for (const declaration of [
      ...(block?.declarations ?? []),
      ...(block?.importantDeclarations ?? []),
    ]) {
      const property = propertyName(declaration);
      if (!hoverProperties.has(property)) {
        problems.push({
          file,
          line: loc.line + 1,
          column: loc.column,
          selector: selectorAt(loc.line, loc.column),
          property,
        });
      }
    }
  };
  const walk = (rules: readonly Rule[], onHover: boolean): void => {
    for (const rule of rules) {
      if (rule.type === "style") {
        const style = rule.value as StyleRule;
        const hover = onHover || mentionsHover(style.selectors);
        if (hover) {
          check(style.declarations, style.loc);
        }
        walk(style.rules ?? [], hover);
        continue;
      }
      const {
        rules: nested,
        declarations,
        loc,
        ...prelude
      } = ("value" in rule ? rule.value : {}) as {
        rules?: Rule[];
        declarations?: DeclarationBlock;
        loc?: StyleRule["loc"];
      };
      // An at-rule whose prelude has a selector that mentions :hover, as
      // @scope (.card:hover), or @scope (.a) to (.b:hover), whose rules a
      // hover brings into the scope or out of it, applies its rules on
      // hover. A condition, as @media (hover: hover), or @supports
      // selector(:hover), has no selector, and a conditional group, as
      // @media, @supports, @layer, or @container, applies its rules as they
      // are.
      const hover = onHover || mentionsHover(prelude);
      if (hover && declarations !== undefined) {
        // Declarations nested in a rule that applies on hover, after its
        // nested rules, as CSS nesting allows.
        check(declarations, loc ?? { line: 0, column: 1 });
      }
      if (Array.isArray(nested)) {
        walk(nested, hover);
      }
    }
  };
  const { warnings } = transform({
    filename: file,
    code: new TextEncoder().encode(css),
    // lightningcss reads past what it cannot, rather than stop at the
    // first, and warns of each, so that the check says where each is.
    errorRecovery: true,
    visitor: {
      StyleSheet(sheet) {
        walk(sheet.rules, false);
      },
    },
  });
  for (const warning of warnings) {
    problems.push({
      file,
      line: warning.loc.line,
      column: warning.loc.column,
      selector: "",
      property: "",
      unread: warning.message,
    });
  }
  return problems;
}

/** Formats a problem as file:line:column: message. */
export function formatHoverProblem(p: HoverProblem): string {
  if (p.unread !== undefined) {
    return (
      `${p.file}:${p.line}:${p.column}: the hover check cannot read this CSS (${p.unread}), so a rule ` +
      "there that applies on hover, which a browser may still apply, would go unchecked (PRD FR-EDIT-09). " +
      "Write it as lightningcss, which the check reads the build with, reads it, or update lightningcss. " +
      "See web/app/lint/hover.ts."
    );
  }
  return (
    `${p.file}:${p.line}:${p.column}: ${p.selector} applies on hover and sets ${p.property}, ` +
    "which can show, hide, move, or resize what it styles, so that a touch screen, which has no hover, " +
    "could not reach it (PRD FR-EDIT-09). A hover state may set only colours, decoration, box-shadow, " +
    "and the cursor, and no custom property, as Tailwind's shadow and ring utilities set: write a " +
    "shadow as hover:[box-shadow:…]. See hoverProperties in web/app/lint/hover.ts."
  );
}

/** Reports whether a selector of the list mentions :hover anywhere. */
function mentionsHover(selectors: unknown): boolean {
  if (Array.isArray(selectors)) {
    return selectors.some(mentionsHover);
  }
  if (selectors === null || typeof selectors !== "object") {
    return false;
  }
  const component = selectors as Record<string, unknown>;
  if (component.type === "pseudo-class" && component.kind === "hover") {
    return true;
  }
  // A pseudo-class that takes selectors, as :is(), :not(), :has(), or
  // :nth-child(… of …), holds them in a field of its own.
  return Object.values(component).some(mentionsHover);
}

/** Returns the name of the property that a declaration sets. */
function propertyName(declaration: {
  property: string;
  value?: unknown;
}): string {
  const value = declaration.value as
    | { propertyId?: { property: string }; name?: string }
    | undefined;
  if (declaration.property === "unparsed" && value?.propertyId) {
    // A declaration whose value has a var() is kept as tokens.
    return value.propertyId.property;
  }
  if (declaration.property === "custom" && typeof value?.name === "string") {
    // A custom property, or a property that lightningcss does not know.
    return value.name;
  }
  return declaration.property;
}

/**
 * Returns the problems of the <style> elements of the page html, at their
 * lines and columns in it.
 */
export function checkStyleElements(html: string, file: string): HoverProblem[] {
  const problems: HoverProblem[] = [];
  for (const element of html.matchAll(
    /(<style\b[^>]*>)([\s\S]*?)<\/style\s*>/gi,
  )) {
    const start = element.index + (element[1] ?? "").length;
    // The page before the element's CSS, as blanks, keeps the lines and
    // columns of the CSS as they are in the page.
    const before = html.slice(0, start).replace(/[^\n]/g, " ");
    problems.push(...checkHover(before + (element[2] ?? ""), file));
  }
  return problems;
}

/**
 * Returns the problems of the stylesheets in dir, the app's public/, and its
 * subdirectories, which Vite copies to the build as they are, each named by
 * its path from root, the app's.
 */
function checkPublic(dir: string, root: string): HoverProblem[] {
  if (dir === "" || !existsSync(dir)) {
    return [];
  }
  return readdirSync(dir, { recursive: true, encoding: "utf8" })
    .filter((name) => /\.css$/i.test(name))
    .sort()
    .map((name) => join(dir, name))
    .filter((path) => statSync(path).isFile())
    .flatMap((path) =>
      checkHover(
        readFileSync(path, "utf8"),
        relative(root, path).replaceAll("\\", "/"),
      ),
    );
}

/**
 * Reports whether id, a module's, imports a file of CSS, or of a language
 * that Vite builds as CSS, as a string, as Vite reads ?inline and ?raw: a
 * script then has it, where the check cannot read it.
 */
export function importsCssAsString(id: string): boolean {
  const query = id.indexOf("?");
  return (
    query >= 0 &&
    /\.(?:css|less|sass|scss|styl|stylus|pcss|postcss|sss)$/.test(
      id.slice(0, query),
    ) &&
    /[?&](?:inline|raw)\b/.test(id.slice(query))
  );
}

/**
 * Returns the Vite plugin that checks the CSS of the app's build, and fails
 * the build on a rule that applies on hover and sets a property that
 * hoverProperties does not list, on CSS that the check cannot read, or on
 * CSS that a script imports as a string.
 */
export function hoverCheck(): Plugin {
  let root = "";
  let publicDir = "";
  return {
    name: "storycreep:hover-check",
    apply: "build",
    enforce: "post",
    configResolved(config) {
      root = config.root;
      publicDir = config.publicDir;
    },
    transform(_, id) {
      if (importsCssAsString(id)) {
        const file = relative(root, id).replaceAll("\\", "/");
        this.error(
          `${file}: CSS that a script imports as a string, with ?inline or ?raw, goes into the script, ` +
            "where the hover check cannot read it, so a rule there that applies on hover would go " +
            "unchecked (PRD FR-EDIT-09). Import it as a stylesheet, which the check reads. See " +
            "web/app/lint/hover.ts.",
        );
      }
    },
    generateBundle(_, bundle) {
      const problems: HoverProblem[] = [];
      for (const output of Object.values(bundle)) {
        if (output.type !== "asset") {
          continue;
        }
        const source =
          typeof output.source === "string"
            ? output.source
            : new TextDecoder().decode(output.source);
        if (/\.css$/i.test(output.fileName)) {
          problems.push(...checkHover(source, output.fileName));
        } else if (/\.html$/i.test(output.fileName)) {
          problems.push(...checkStyleElements(source, output.fileName));
        }
      }
      problems.push(...checkPublic(publicDir, root));
      if (problems.length > 0) {
        this.error(problems.map(formatHoverProblem).join("\n"));
      }
    },
  };
}
