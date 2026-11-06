import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { RichTextDoc } from "@storycreep/shared/api";
import { parseRichText } from "@storycreep/shared/richtext";
import type { Node as PMNode } from "@tiptap/pm/model";
import {
  AllSelection,
  NodeSelection,
  type Selection,
  TextSelection,
} from "@tiptap/pm/state";
import { describe, expect, test, vi } from "vitest";
import { testEditor } from "../test/editor";
import { storedOf } from "./convert";
import { cleanPastedHTML } from "./paste";

/** Returns a file of testdata/: what Word, Word Online, and Google Docs put on the clipboard, as Chrome reads it. */
function fixture(name: string): string {
  return readFileSync(join(import.meta.dirname, "testdata", name), "utf8");
}

/** Returns what pasting html into an empty editor makes, as the server stores it. */
function pasted(html: string, editor = testEditor()): RichTextDoc {
  editor.commands.clearContent();
  editor.view.pasteHTML(html);
  const value = storedOf(editor.state.doc);
  if (value.error !== undefined) {
    throw value.error;
  }
  return value.doc;
}

const text = (value: string, ...marks: string[]) => ({
  ...(marks.length === 0 ? {} : { marks: marks.map((type) => ({ type })) }),
  text: value,
  type: "text",
});
const linked = (value: string, href: string) => ({
  marks: [{ attrs: { href }, type: "link" }],
  text: value,
  type: "text",
});
const p = (...content: object[]) =>
  content.length === 0 ? { type: "paragraph" } : { content, type: "paragraph" };
const item = (...content: object[]) => ({ content, type: "listItem" });
const bullets = (...items: object[]) => ({
  content: items,
  type: "bulletList",
});
const numbers = (start: number, ...items: object[]) => ({
  ...(start === 1 ? {} : { attrs: { start } }),
  content: items,
  type: "orderedList",
});
const doc = (...content: object[]) => ({ content, type: "doc" });

describe("pasting", () => {
  // Verifies: FR-TEXT-01/AC2
  test("Google Docs keeps its bold, italic, and struck-through text, lists, and links, and makes plain text of the rest", () => {
    expect(pasted(fixture("google-docs.html"))).toEqual(
      doc(
        p(
          text("The keeper said: "),
          text("stay", "bold"),
          text(" "),
          text("away", "italic"),
          text(" "),
          text("now", "strike"),
          text(", under a sea"),
        ),
        p(text("Packing")),
        p(),
        bullets(
          item(p(text("Rope", "bold")), bullets(item(p(text("Oil"))))),
          item(p(text("Lamp"))),
        ),
        numbers(3, item(p(text("Sail")))),
        p(
          text("Ask at "),
          linked("the harbour", "https://example.com/harbour"),
          text(" or see Packing"),
        ),
        p(text("Tide")),
        p(text("High")),
      ),
    );
  });

  // Verifies: FR-TEXT-01/AC2
  test("Word keeps its marks, and makes lists of its paragraphs of a list, numbered from where Word numbers them", () => {
    expect(pasted(fixture("word.html"))).toEqual(
      doc(
        p(
          text("Bold", "bold"),
          text(" and "),
          text("italic", "italic"),
          text(" and "),
          text("struck", "strike"),
          text(" and underlined and red, "),
          {
            marks: [{ type: "bold" }, { type: "italic" }],
            text: "both",
            type: "text",
          },
          text("."),
        ),
        p(),
        bullets(
          item(p(text("Rope")), bullets(item(p(text("Oil"))))),
          item(p(text("Lamp"))),
        ),
        numbers(3, item(p(text("Sail"))), item(p(text("Return")))),
        p(text("Arrival")),
        p(
          text("See "),
          linked("the site", "https://example.com/"),
          text(" and a file."),
          { type: "hardBreak" },
          text("Next line"),
        ),
        p(text("Shown")),
        p(text("Tide")),
        p(text("High")),
      ),
    );
  });

  // Verifies: FR-TEXT-01/AC2
  test("Word Online's lists of one item each are one list, nested by their levels", () => {
    expect(pasted(fixture("word-online.html"))).toEqual(
      doc(
        p(text("Bold", "bold"), text(" plain "), text("slanted", "italic")),
        bullets(item(p(text("First")), bullets(item(p(text("Nested")))))),
        numbers(1, item(p(text("Numbered")))),
      ),
    );
  });

  // Verifies: FR-TEXT-01/AC2
  test("HTML that the schema does not hold gives plain text, and none of its scripts, styles, frames, or links that a link may not lead to", () => {
    const html = `<script>alert(1)</script><style>p{color:red}</style>
      <p onclick="steal()">Click <img src="x" onerror="alert(1)">me</p>
      <iframe src="https://evil.example/"></iframe>
      <p><a href="javascript:alert(1)">script</a> <a href="data:text/html,x">data</a> <a href="https://example.com@evil.example/">userinfo</a> <a href="/relative">relative</a> <a href=" https://café.example/menu ">café</a> <a href="MAILTO:keeper@example.com">mail</a></p>
      <table><tr><th>Name</th><td>Elara</td></tr></table>
      <blockquote><p>Quoted</p></blockquote>
      <pre>let tide = 1;
if (tide) {}</pre>
      <h3>Title</h3>
      <p><u>under</u> <sup>2</sup> <font color="red">font</font> <mark>marked</mark> <code><b>code</b></code> <a href="https://example.com/"><code>linked code</code></a></p>
      <svg><text>drawn</text></svg><math><mi>x</mi></math><template><p>template</p></template>
      <noscript>no script</noscript><object>object</object><select><option>option</option></select>
      <textarea>area</textarea><button>button</button><video>video</video>
      <div hidden>hidden</div><p style="display:none">not shown</p>
      <p>Mark&#x200F;ed &#x202E;over&#x202C;ridden&#x2028;line</p>
      <p>Ruby <ruby>漢<rp>(</rp><rt>kan</rt><rp>)</rp></ruby></p>
      <p><span data-type="mention" data-id="nd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD">@not a component</span> and <span data-type="mention" data-id="u123">@Bob</span></p>`;
    expect(pasted(html)).toEqual(
      doc(
        p(text("Click me")),
        p(
          text("script data userinfo relative "),
          linked("café", "https://xn--caf-dma.example/menu"),
          text(" "),
          linked("mail", "MAILTO:keeper@example.com"),
        ),
        p(text("Name")),
        p(text("Elara")),
        p(text("Quoted")),
        p(
          text("let tide = 1;", "code"),
          { type: "hardBreak" },
          text("if (tide) {}", "code"),
        ),
        p(text("Title")),
        p(
          text("under 2 font marked "),
          text("code", "code"),
          text(" "),
          linked("linked code", "https://example.com/"),
        ),
        p(text("Marked overridden"), { type: "hardBreak" }, text("line")),
        p(text("Ruby 漢")),
        p(text("@not a component and @Bob")),
      ),
    );
  });

  // Verifies: FR-TEXT-01/AC2
  test("another editor's table rows, which ProseMirror copies with the elements that wrap them, give paragraphs of all their text", () => {
    expect(
      pasted(
        '<table><tbody><tr data-pm-slice="1 1 -2 []"><td><p>first <strong>cell</strong> text</p></td><td><p>second</p></td></tr></tbody></table>',
      ),
    ).toEqual(
      doc(
        p(text("first "), text("cell", "bold"), text(" text")),
        p(text("second")),
      ),
    );
  });

  test("a pre keeps the indentation of its lines", () => {
    expect(pasted("<pre>if (tide) {\n    sail();\n\t}</pre>")).toEqual(
      doc(
        p(
          text("if (tide) {", "code"),
          { type: "hardBreak" },
          text("    sail();", "code"),
          { type: "hardBreak" },
          text("\t}", "code"),
        ),
      ),
    );
  });

  test("text whose white-space style keeps its white space, as Google Docs's and a code editor's, keeps it", () => {
    const docs = (value: string) =>
      `<span style="font-weight:400;white-space:pre;white-space:pre-wrap;">${value}</span>`;
    expect(
      pasted(
        `<b style="font-weight:normal;" id="docs-internal-guid-1"><p dir="ltr">${docs("\tOnce  upon")}${docs(" a ")}<span>tide,  then</span></p></b>` +
          '<div style="white-space: pre;"><div><span>if (tide) {</span></div><div>    <span>sail();</span></div><div style="white-space: normal">  }  </div></div>',
      ),
    ).toEqual(
      doc(
        p(text("\tOnce  upon a tide, then")),
        p(text("if (tide) {")),
        p(text("    sail();")),
        p(text("}")),
      ),
    );
  });

  test("text whose white-space style is pre-line keeps its line breaks, and collapses the rest of its white space, as a browser shows it", () => {
    expect(
      pasted(
        '<div style="white-space:pre-line"><p>line  one  \n   line two</p><p><span>a</span>\n<span>b</span></p><p style="white-space:normal">c\nd</p></div>',
      ),
    ).toEqual(
      doc(
        p(text("line one"), { type: "hardBreak" }, text("line two")),
        p(text("a"), { type: "hardBreak" }, text("b")),
        p(text("c d")),
      ),
    );
  });

  test("the rest of the white space of a paragraph that holds kept white space is collapsed, as a browser shows it", () => {
    expect(
      pasted(
        '<p><span style="white-space:pre">  a  </span>   b \n  c <br>  d</p>',
      ),
    ).toEqual(doc(p(text("  a  b c "), { type: "hardBreak" }, text("d"))));
  });

  // Verifies: FR-TEXT-01/AC2
  test("styles are read without a style attribute or a style sheet, which the page's policy refuses in a document that DOMParser makes too, and the cleaned HTML has neither", () => {
    const parse = vi.spyOn(DOMParser.prototype, "parseFromString");
    const cleaned: string[] = [];
    let parsed: string[] = [];
    try {
      for (const name of [
        "google-docs.html",
        "word-online.html",
        "word.html",
      ]) {
        cleaned.push(cleanPastedHTML(fixture(name)));
      }
      expect(
        pasted(
          `<STYLE>p { color: red }</STYLE ><P STYLE="font-weight:bold">a</P><p style='font-style:italic'>b</p><p style=text-decoration:line-through>c</p>` +
            `<!-- <style> --><p title=' style="x" <style>'>d style="e"</p><textarea style="x"><p style="y"></textarea><p>f</p>`,
        ),
      ).toEqual(
        doc(
          p(text("a", "bold")),
          p(text("b", "italic")),
          p(text("c", "strike")),
          p(text('d style="e"')),
          p(text("f")),
        ),
      );
      parsed = parse.mock.calls.map(([html]) => html);
    } finally {
      parse.mockRestore();
    }
    // Neither what the cleaning parsed nor what it wrote has a style
    // attribute or a style element.
    const styled = (html: string) =>
      new DOMParser()
        .parseFromString(html, "text/html")
        .querySelector("[style], style") !== null;
    expect(parsed.length).toBeGreaterThanOrEqual(4);
    expect(parsed.filter(styled)).toEqual([]);
    expect(cleaned.filter(styled)).toEqual([]);
  });

  test("style attributes and style elements are found where HTML's tokenizer finds them, so that the parsed HTML has neither, and the rest is read as HTML reads it", () => {
    const bold = '<p style="font-weight:bold">a</p>';
    const shapes = [
      // Comments that ">" or "->" right after "<!--", or "--!>", end.
      `<!-->${bold}`,
      `<!--->${bold}`,
      `<!-- x --!>${bold}`,
      // A name right after a quoted value, and a quote within a value that
      // is not quoted, which begins nothing.
      '<p title="x"style="font-weight:bold">a</p>',
      `<p title=x"y style="font-weight:bold">a</p><p title='z'>`,
      // White space that HTML does not read as such, which ends no name.
      `<script\u00a0>${bold}</script\u00a0>`,
      // What HTML reads as a comment up to its first ">", and the attributes
      // of an end tag, which hold what is no tag.
      `<!x <script>${bold}`,
      `<? <style>${bold}`,
      `</x a="<script>">${bold}`,
      // An SVG title, which holds HTML, which the cleaning leaves out.
      `<svg><title><b style="x">t</b><style>b{}</style></title></svg>${bold}`,
      // The text of an element of text that HTML reads as HTML, within SVG
      // or a select, whose end HTML reads within a value, and that of a
      // script whose "<!--<script" takes it past its first end tag.
      `<svg><title><b title="</title>" style="x">t</b></svg>${bold}`,
      `<select><title><option title="</title>" style="x"></select>${bold}`,
      `<script><!--<script></script><b title="</script><b style=x>">${bold}`,
      // An xmp within SVG, whose text HTML reads as HTML.
      `<svg><xmp><style>*{}</style><b style="font-style:italic">x</b></xmp></svg>${bold}`,
    ];
    const parse = vi.spyOn(DOMParser.prototype, "parseFromString");
    let parsed: string[] = [];
    try {
      for (const html of shapes) {
        expect(pasted(html), html).toEqual(doc(p(text("a", "bold"))));
      }
      // A "<" before a style element begins no tag with what follows it.
      expect(
        pasted(
          `<<style></style>textarea><!--</textarea><b style="x">-->${bold}`,
        ),
      ).toEqual(doc(p(text("<textarea>")), p(text("a", "bold"))));
      // A plaintext within MathML, and an xmp within a frameset, of which
      // HTML reads the text as HTML, show nothing, and an xmp elsewhere
      // shows its text.
      expect(pasted('<math><plaintext><b style="x">t</b>')).toEqual(doc(p()));
      expect(pasted('<frameset><xmp><frame style="x"></xmp>')).toEqual(
        doc(p()),
      );
      expect(pasted('<xmp><b style="x">t</b></xmp>')).toEqual(
        doc(p(text('<b style="x">t</b>'))),
      );
      // A value that is not quoted holds what follows it.
      expect(
        pasted(
          "<p><a href=https://example.com/style/guide>a</a> <a href=https://example.com/style>b</a></p>",
        ),
      ).toEqual(
        doc(
          p(
            linked("a", "https://example.com/style/guide"),
            text(" "),
            linked("b", "https://example.com/style"),
          ),
        ),
      );
      parsed = parse.mock.calls.map(([html]) => html);
    } finally {
      parse.mockRestore();
    }
    expect(
      parsed.filter(
        (html) =>
          new DOMParser()
            .parseFromString(html, "text/html")
            .querySelector("[style], style") !== null,
      ),
    ).toEqual([]);
  });

  test("random HTML of tags, quotes, comments, and elements of text is parsed without a style attribute or a style element, and otherwise as HTML parses it", () => {
    // A seeded generator (mulberry32), so that a failure is the same on
    // every run.
    let seed = 64;
    const random = () => {
      seed = (seed + 0x6d2b79f5) | 0;
      let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4_294_967_296;
    };
    const fragments = [
      ...["<", ">", "</", "<!", "<?", "<!--", "-->", "--!>", "<!-->"],
      ...['"', "'", "=", "/", " ", "\t", "\n", "\u00a0", "-", "!", "x"],
      ...["style", "STYLE", "style=", " style=x", "/style", "p", "b"],
      ...["script", "textarea", "title", "xmp", "noembed", "noscript"],
      ...["<p ", "<style>", "</style>", "<script>", "</script>"],
      ...["<title>", "</title>", "<![CDATA[", "]]>", "<a href=x/style>"],
    ];
    /** Returns the document of html as the cleaning should parse it: without its style elements, and with its style attributes named as the cleaning reads them, of which the first gives the value. */
    const expected = (html: string) => {
      const parsed = new DOMParser().parseFromString(html, "text/html");
      for (const style of parsed.querySelectorAll("style")) {
        style.remove();
      }
      for (const element of parsed.querySelectorAll("[style]")) {
        const attributes = Array.from(element.attributes, (a) => [
          a.name === "style" ? "data-pasted-style" : a.name,
          a.value,
        ]);
        for (const { name } of Array.from(element.attributes)) {
          element.removeAttribute(name);
        }
        for (const [name, value] of attributes) {
          if (!element.hasAttribute(name)) {
            element.setAttribute(name, value);
          }
        }
      }
      return parsed;
    };
    /** Returns the HTML of a document's body, which the cleaning reads, without the text of the elements whose text it leaves out, which the cleaning leaves out before it parses the HTML, or the comments, as the one that a style element leaves. */
    const shown = (parsed: Document) => {
      for (const element of parsed.querySelectorAll(
        "script, textarea, title, iframe, noembed, noframes",
      )) {
        element.textContent = "";
      }
      const comments = parsed.createTreeWalker(
        parsed.body,
        NodeFilter.SHOW_COMMENT,
      );
      const found: Node[] = [];
      while (comments.nextNode() !== null) {
        found.push(comments.currentNode);
      }
      for (const comment of found) {
        comment.parentNode?.removeChild(comment);
      }
      parsed.body.normalize();
      return parsed.body.outerHTML;
    };
    const parse = vi.spyOn(DOMParser.prototype, "parseFromString");
    const cases: { html: string; parsed: string }[] = [];
    try {
      for (let i = 0; i < 200; i++) {
        const html = Array.from(
          { length: 3 + Math.floor(random() * 25) },
          () => fragments[Math.floor(random() * fragments.length)],
        ).join("");
        parse.mockClear();
        cleanPastedHTML(html);
        cases.push({ html, parsed: parse.mock.calls[0][0] });
      }
    } finally {
      parse.mockRestore();
    }
    for (const { html, parsed } of cases) {
      const result = new DOMParser().parseFromString(parsed, "text/html");
      expect(result.querySelector("[style], style"), html).toBeNull();
      expect(shown(result), html).toBe(shown(expected(html)));
    }
  });

  test("a list item's level is the first of its aria-level and data-aria-level that is a level", () => {
    expect(
      pasted(
        '<ul><li>a</li><li aria-level="" data-aria-level="2">b</li><li aria-level="x" data-aria-level="1">c</li></ul>',
      ),
    ).toEqual(
      doc(
        bullets(
          item(p(text("a")), bullets(item(p(text("b"))))),
          item(p(text("c"))),
        ),
      ),
    );
  });

  test("a paragraph of Word's list at level 0 is an item at the first level, and a list's start is read as HTML reads it", () => {
    expect(
      pasted(
        '<p style="mso-list:l0 level0 lfo1"><span style="mso-list:Ignore">1.</span>a</p>' +
          '<p>b</p><ol start="5a"><li>c</li></ol><ol start=" 7.0"><li>d</li></ol>',
      ),
    ).toEqual(
      doc(
        numbers(1, item(p(text("a")))),
        p(text("b")),
        numbers(5, item(p(text("c")))),
        numbers(7, item(p(text("d")))),
      ),
    );
  });

  test("HTML of which the cleaning keeps nothing, as of only a button, is pasted as the clipboard's plain text", () => {
    const editor = testEditor();
    const event = Object.assign(new Event("paste"), {
      clipboardData: {
        getData: (type: string) => (type === "text/plain" ? "Save" : ""),
      },
    }) as unknown as ClipboardEvent;
    editor.view.pasteHTML("<button>Save</button>", event);
    expect(storedOf(editor.state.doc).doc).toEqual(doc(p(text("Save"))));
  });

  test("a paste of which nothing is kept, as of only an image, leaves the selection, and one of only white space pastes it over the selection", () => {
    const editor = testEditor();
    const clipboard = (plain: string) =>
      Object.assign(new Event("paste"), {
        clipboardData: {
          getData: (type: string) => (type === "text/plain" ? plain : ""),
        },
      }) as unknown as ClipboardEvent;
    editor.commands.setContent(doc(p(text("Tide and shore"))));
    editor.commands.setTextSelection({ from: 5, to: 9 });
    editor.view.pasteHTML(
      '<img src="https://example.com/a.png">',
      clipboard(""),
    );
    expect(storedOf(editor.state.doc).doc).toEqual(
      doc(p(text("Tide and shore"))),
    );
    editor.view.pasteHTML("<span> </span>", clipboard(" "));
    expect(storedOf(editor.state.doc).doc).toEqual(doc(p(text("Tide  shore"))));
  });

  test("a paragraph loses the line break that ends it, though white space follows it, and Word's line break that ends a paragraph, which Word writes twice, is one", () => {
    expect(
      pasted(
        "<p class=MsoNormal>Tide<br>\r\n<![if !supportLineBreakNewLine]><br>\r\n<![endif]><o:p></o:p></p>" +
          "<p>High<br> </p><p>Low<br>&nbsp;</p><p>Ebb <br></p><p>Flood<br><br></p>",
      ),
    ).toEqual(
      doc(
        p(text("Tide"), { type: "hardBreak" }),
        p(text("High")),
        p(text("Low")),
        p(text("Ebb")),
        p(text("Flood"), { type: "hardBreak" }),
      ),
    );
  });

  // Verifies: FR-TEXT-01/AC2
  test("what a browser does not show, a dialog that is not open, a closed details's content, and the HTML of noembed and noframes, is left out", () => {
    expect(
      pasted(
        "<dialog><p>secret</p></dialog><dialog open><p>open dialog</p></dialog>" +
          "<details><summary>Folded</summary><p>folded away</p></details>" +
          "<details open><summary>Unfolded</summary><p>shown</p></details>" +
          "<noembed><p>embed</p></noembed><noframes><p>frames</p></noframes>" +
          '<div style="visibility:hidden"><p>unseen</p><p>not <span style="visibility:visible">seen</span> but</p></div>' +
          '<p style="visibility:hidden"><b>hidden</b></p><p>end</p>',
      ),
    ).toEqual(
      doc(
        p(text("open dialog")),
        p(text("Folded")),
        p(text("Unfolded")),
        p(text("shown")),
        p(text("seen")),
        p(text("end")),
      ),
    );
  });

  // Verifies: FR-TEXT-01/AC2
  test("random HTML of elements, styles, and attributes that the schema does not hold always gives a document that the server stores", () => {
    // A seeded generator, so that a failure is the same on every run.
    let seed = 57;
    const random = () => {
      seed = (seed * 1_103_515_245 + 12_345) % 2_147_483_648;
      return seed / 2_147_483_648;
    };
    const pick = <T>(items: readonly T[]): T =>
      items[Math.floor(random() * items.length)];
    const tags = [
      "p",
      "div",
      "span",
      "b",
      "strong",
      "i",
      "em",
      "s",
      "del",
      "u",
      "code",
      "pre",
      "a",
      "ul",
      "ol",
      "li",
      "h1",
      "blockquote",
      "table",
      "tr",
      "td",
      "br",
      "img",
      "script",
      "style",
      "sup",
      "font",
    ];
    const styles = [
      "font-weight:700",
      "font-weight:normal",
      "font-style:italic",
      "text-decoration:line-through",
      "display:none",
      "mso-list:l0 level3 lfo1",
      "mso-list:l0 level0 lfo1",
      "white-space:pre-wrap",
      "color:red",
      "white-space:pre",
    ];
    const hrefs = [
      "https://example.com/",
      "javascript:x()",
      "mailto:a@b.example",
      "https://exa mple.com/",
      "https://ex​ample.com/",
      "https://例え.jp/",
      "#top",
      "",
    ];
    const texts = [
      "word",
      " ",
      " ",
      "line break",
      "tab\there",
      "rtl‮over",
      "🐴",
      "\r\n",
      "<b>not a tag</b>",
    ];
    const element = (depth: number): string => {
      if (depth > 9 || random() < 0.3) {
        return pick(texts).replace(
          /[<>&]/g,
          (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;" })[c] ?? c,
        );
      }
      const tag = pick(tags);
      const attrs = [
        random() < 0.4 ? ` style="${pick(styles)}"` : "",
        tag === "a" ? ` href="${pick(hrefs)}"` : "",
        tag === "ol"
          ? ` start="${pick(["0", "-4", "3", "99999999", "x"])}"`
          : "",
        tag === "li" && random() < 0.5
          ? ` aria-level="${pick(["1", "2", "7", "0"])}"`
          : "",
      ].join("");
      const children = Array.from({ length: Math.floor(random() * 4) }, () =>
        element(depth + 1),
      );
      return `<${tag}${attrs}>${children.join("")}</${tag}>`;
    };
    const editor = testEditor();
    for (let i = 0; i < 300; i++) {
      const html = Array.from({ length: 3 }, () => element(0)).join("");
      const stored = pasted(html, editor);
      // The server would take the document as the editor writes it.
      expect(parseRichText(stored), html).toEqual(stored);
    }
  }, 30_000);

  // Verifies: FR-TEXT-01/AC2
  test("lists nested more deeply than lists nest have their items moved up to the deepest level", () => {
    let html = "<p>last</p>";
    for (let depth = 8; depth >= 1; depth--) {
      html = `<ul><li>level ${depth}${html}</li></ul>`;
    }
    const stored = pasted(html.replace("<p>last</p>", ""));
    let list = stored.content[0] as { content: { content: object[] }[] };
    for (let depth = 1; depth < 5; depth++) {
      list = list.content[0].content[1] as typeof list;
    }
    // The fifth list holds the items of levels 5 to 8, one after another.
    expect(list).toEqual(
      bullets(...[5, 6, 7, 8].map((n) => item(p(text(`level ${n}`))))),
    );
  });

  test("a mention and a value reference that the editor copied are pasted as they were", () => {
    const mention = "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BVD";
    const variable = "var_01HQ8Z3XK4F7NAWR5TCQ9M2BVE";
    expect(
      pasted(
        `<p data-pm-slice="1 1 []">Ask <span data-type="mention" data-id="${mention}" class="rich-text-chip">@Elara</span> for <strong><span data-type="value-ref" data-ref="${variable}" data-decimals="2">{gold}</span></strong></p>`,
      ),
    ).toEqual(
      doc(
        p(
          text("Ask "),
          { attrs: { id: mention }, type: "mention" },
          text(" for "),
          {
            attrs: { decimals: 2, ref: variable },
            marks: [{ type: "bold" }],
            type: "valueRef",
          },
        ),
      ),
    );
  });

  describe("what the editor copies", () => {
    /** Returns the HTML that the editor writes to the clipboard of a copy of what select selects of content, as its copy writes it, of the selection's content. */
    function copied(
      content: object,
      select: (doc: PMNode) => Selection,
    ): string {
      const source = testEditor(content as RichTextDoc);
      return source.view.serializeForClipboard(
        select(source.state.doc).content(),
      ).dom.innerHTML;
    }

    /** Returns what pasting html over at to to of content makes, with the editor's props for what is pasted, or with editorProps. */
    function pastedAt(
      html: string,
      content: object,
      at: number,
      to = at,
      editorProps?: object,
    ): RichTextDoc | undefined {
      const target = testEditor(content as RichTextDoc, editorProps);
      target.view.dispatch(
        target.state.tr.setSelection(
          TextSelection.create(target.state.doc, at, to),
        ),
      );
      target.view.pasteHTML(html);
      return storedOf(target.state.doc).doc;
    }

    /** Returns what copying from to to of content, and pasting it at at, makes. */
    function copiedAndPasted(
      content: object,
      from: number,
      to: number,
      at: number,
    ): RichTextDoc | undefined {
      return pastedAt(
        copied(content, (d) => TextSelection.create(d, from, to)),
        content,
        at,
      );
    }

    test("is pasted within a paragraph where it was part of one, though it starts with a mark", () => {
      // "bold te" of the first paragraph, pasted after "Hello ".
      expect(
        copiedAndPasted(
          doc(p(text("bold", "bold"), text(" text")), p(text("Hello world"))),
          1,
          8,
          18,
        ),
      ).toEqual(
        doc(
          p(text("bold", "bold"), text(" text")),
          p(text("Hello "), text("bold", "bold"), text(" teworld")),
        ),
      );
    });

    test("a mention that was selected as a whole is pasted within the paragraph", () => {
      const mention = {
        attrs: { id: "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BVD" },
        type: "mention",
      };
      const content = doc(
        p(text("Ask "), mention, text(" now")),
        p(text("Hello world")),
      );
      // The mention, at 5, pasted after "Hello".
      const html = copied(content, (d) => NodeSelection.create(d, 5));
      expect(pastedAt(html, content, 17)).toEqual(
        doc(
          p(text("Ask "), mention, text(" now")),
          p(text("Hello"), mention, text(" world")),
        ),
      );
    });

    test("joins the item that it is pasted at the end of, where it was cut from within a list's items", () => {
      // From within "aaaa" to within "bbbb", pasted at the end of "bbbb".
      expect(
        copiedAndPasted(
          doc(bullets(item(p(text("aaaa"))), item(p(text("bbbb"))))),
          5,
          13,
          15,
        ),
      ).toEqual(
        doc(
          bullets(
            item(p(text("aaaa"))),
            item(p(text("bbbbaa"))),
            item(p(text("bb"))),
          ),
        ),
      );
    });

    test("keeps the lists within a list item that it was cut from within, and their kind and numbering", () => {
      // From within "aaaa" to within "cccc", the item of a list within it,
      // pasted at the end of "zzzz".
      const nested = doc(
        bullets(
          item(p(text("aaaa")), bullets(item(p(text("cccc"))))),
          item(p(text("zzzz"))),
        ),
      );
      expect(copiedAndPasted(nested, 4, 13, 25)).toEqual(
        doc(
          bullets(
            item(p(text("aaaa")), bullets(item(p(text("cccc"))))),
            item(p(text("zzzzaaa")), bullets(item(p(text("cc"))))),
          ),
        ),
      );
      // The same of a list numbered from 3, pasted at the start of "XXYY".
      const numbered = doc(
        numbers(3, item(p(text("aaaa")), numbers(1, item(p(text("cccc")))))),
        p(text("XXYY")),
      );
      expect(copiedAndPasted(numbered, 4, 13, 21)).toEqual(
        doc(
          numbers(3, item(p(text("aaaa")), numbers(1, item(p(text("cccc")))))),
          numbers(3, item(p(text("aaa")), numbers(1, item(p(text("ccXXYY")))))),
        ),
      );
    });

    test("keeps its empty paragraphs and the white space at a paragraph's end", () => {
      const content = doc(p(text("a  ")), p(), p(text("b")));
      expect(
        pastedAt(
          copied(content, (d) => new AllSelection(d)),
          content,
          9,
        ),
      ).toEqual(
        doc(
          p(text("a  ")),
          p(),
          p(text("b")),
          p(text("a  ")),
          p(),
          p(text("b")),
        ),
      );
    });

    test("keeps a list item that it was cut from within a list within, which lacks its first paragraph, and lists side by side", () => {
      // From within "bb", of a list within the first item, to within "cc",
      // pasted over itself, and at the end of "end".
      const nested = doc(
        bullets(
          item(p(text("a")), bullets(item(p(text("bb"))))),
          item(p(text("cc"))),
        ),
        p(text("end")),
      );
      const html = copied(nested, (d) => TextSelection.create(d, 9, 17));
      expect(pastedAt(html, nested, 9, 17)).toEqual(nested);
      expect(pastedAt(html, nested, 25)).toEqual(
        doc(
          bullets(
            item(p(text("a")), bullets(item(p(text("bb"))))),
            item(p(text("cc"))),
          ),
          p(text("endb")),
          bullets(item(p(text("c")))),
        ),
      );
      // From the start of "aa" to the end of "bb", of two lists side by
      // side, pasted at the end of "bb", whose list the second list follows.
      const sideBySide = doc(
        bullets(item(p(text("aa")))),
        bullets(item(p(text("bb")))),
      );
      expect(
        pastedAt(
          copied(sideBySide, (d) => TextSelection.create(d, 3, 13)),
          sideBySide,
          13,
        ),
      ).toEqual(
        doc(
          bullets(item(p(text("aa")))),
          bullets(item(p(text("bbaa")))),
          bullets(item(p(text("bb")))),
        ),
      );
    });

    test("of random documents and selections is pasted as ProseMirror pastes its own HTML", () => {
      // A seeded generator, so that a failure is the same on every run.
      let seed = 64;
      const random = () => {
        seed = (seed * 1_103_515_245 + 12_345) % 2_147_483_648;
        return seed / 2_147_483_648;
      };
      const pick = <T>(items: readonly T[]): T =>
        items[Math.floor(random() * items.length)];
      const inline = () =>
        text(
          pick(["ab", "cd ", " ef", "g"]),
          ...(random() < 0.3 ? [pick(["bold", "italic", "code"])] : []),
        );
      const block = (depth: number): object => {
        if (depth < 3 && random() < 0.4) {
          const items = Array.from(
            { length: 1 + Math.floor(random() * 3) },
            () =>
              item(p(inline()), ...(random() < 0.4 ? [block(depth + 1)] : [])),
          );
          return random() < 0.5
            ? bullets(...items)
            : numbers(pick([1, 3]), ...items);
        }
        return random() < 0.1
          ? p()
          : p(...Array.from({ length: 1 + Math.floor(random() * 2) }, inline));
      };
      for (let i = 0; i < 200; i++) {
        const content = doc(
          ...Array.from({ length: 2 + Math.floor(random() * 3) }, () =>
            block(0),
          ),
        );
        // The places within the document's paragraphs.
        const places: number[] = [];
        testEditor(content as RichTextDoc).state.doc.descendants(
          (node, pos) => {
            for (let k = 0; node.isTextblock && k <= node.content.size; k++) {
              places.push(pos + 1 + k);
            }
          },
        );
        const [from, to] = [pick(places), pick(places)].sort((a, b) => a - b);
        if (from === to) {
          continue;
        }
        const html = copied(content, (d) => TextSelection.create(d, from, to));
        const at = pick(places);
        for (const [a, b] of [
          [from, to],
          [at, at],
        ]) {
          expect(pastedAt(html, content, a, b), html).toEqual(
            pastedAt(html, content, a, b, {}),
          );
        }
      }
    }, 30_000);

    test("another editor's slice that begins with what the cleaning leaves out, as an image, begins with what follows it", () => {
      // The paragraph is closed at its start, after the image, and so is a
      // paragraph of its own; it is open at its end, and so joins the rest
      // of the paragraph that it is pasted into.
      expect(
        pastedAt(
          '<img data-pm-slice="0 1 []" src="x"><p>text</p>',
          doc(p(text("Hello world"))),
          7,
        ),
      ).toEqual(doc(p(text("Hello ")), p(text("textworld"))));
    });
  });

  test("a numbered list that is not numbered from where the one before it ends is a list of its own, and Word's nested items are numbered by their own numbers", () => {
    expect(
      pasted(
        '<ol><li>a</li><li>b</li></ol><ol start="10"><li>x</li></ol><ol start="11"><li>y</li></ol><ol><li>z</li></ol>',
      ),
    ).toEqual(
      doc(
        numbers(1, item(p(text("a"))), item(p(text("b")))),
        numbers(10, item(p(text("x"))), item(p(text("y")))),
        numbers(1, item(p(text("z")))),
      ),
    );
    const word = (level: number, marker: string, value: string) =>
      `<p style="mso-list:l0 level${level} lfo1"><span style="mso-list:Ignore">${marker}<span style="font:7.0pt">&nbsp;&nbsp;</span></span>${value}</p>`;
    expect(
      pasted(
        word(1, "1.", "a") +
          word(2, "1.1.", "b") +
          word(2, "1.2.", "c") +
          word(1, "2.", "d") +
          word(2, "2.1.", "e"),
      ),
    ).toEqual(
      doc(
        numbers(
          1,
          item(
            p(text("a")),
            numbers(1, item(p(text("b"))), item(p(text("c")))),
          ),
          item(p(text("d")), numbers(1, item(p(text("e"))))),
        ),
      ),
    );
  });

  test("a list within a list item that no list holds is nested within it", () => {
    expect(pasted("<li>aaa<ul><li>cc</li></ul></li>")).toEqual(
      doc(bullets(item(p(text("aaa")), bullets(item(p(text("cc"))))))),
    );
  });

  test("plain text loses the characters that a text may not hold, and each line is a paragraph", () => {
    const editor = testEditor();
    editor.view.pasteText("first‏ line\u0007\nsecond\ttabbed");
    expect(storedOf(editor.state.doc).doc).toEqual(
      doc(p(text("first line")), p(text("second\ttabbed"))),
    );
  });
});
