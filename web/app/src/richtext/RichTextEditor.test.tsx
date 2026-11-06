import type { RichTextDoc } from "@storycreep/shared/api";
import { MAX_CHARS } from "@storycreep/shared/richtext";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { describe, expect, test, vi } from "vitest";
import { createAppI18n } from "../i18n";
import "../test/editor";
import { useHeld } from "../ui/held";
import type { RichTextValue } from "./convert";
import { RichTextEditor, type RichTextEditorProps } from "./RichTextEditor";

const paragraph = (text: string): RichTextDoc => ({
  content: [{ content: [{ text, type: "text" }], type: "paragraph" }],
  type: "doc",
});

/** Renders the editor of props in English, and returns what each change gave the page. */
function renderEditor(props: Partial<RichTextEditorProps> = {}) {
  const changes: RichTextValue[] = [];
  const onChange = vi.fn((value: RichTextValue) => {
    changes.push(value);
  });
  render(
    <I18nextProvider i18n={createAppI18n(["en"], document.documentElement)}>
      <RichTextEditor label="Body" onChange={onChange} {...props} />
    </I18nextProvider>,
  );
  const textbox = screen.getByRole("textbox", { name: "Body" });
  return { changes, textbox, last: () => changes.at(-1) };
}

/** Selects the whole text of the editor that has the focus, as its keyboard does. */
async function selectAll(textbox: HTMLElement) {
  textbox.focus();
  await userEvent.keyboard("{Control>}a{/Control}");
}

describe("the rich-text editor", () => {
  test("is a labelled text box, whose toolbar's buttons say whether the selection has their mark", async () => {
    const { textbox, last } = renderEditor({
      defaultValue: paragraph("Stay away"),
      hint: "What the keeper says",
    });
    expect(textbox.getAttribute("aria-multiline")).toBe("true");
    screen.getByRole("textbox", {
      name: "Body",
      description: "What the keeper says",
    });
    screen.getByRole("toolbar", { name: "Formatting of Body" });
    const bold = screen.getByRole("button", { name: "Bold", pressed: false });
    await selectAll(textbox);
    await userEvent.click(bold);
    expect(last()?.doc).toEqual({
      content: [
        {
          content: [
            { marks: [{ type: "bold" }], text: "Stay away", type: "text" },
          ],
          type: "paragraph",
        },
      ],
      type: "doc",
    });
    await waitFor(() => expect(bold.getAttribute("aria-pressed")).toBe("true"));
    await userEvent.click(
      screen.getByRole("button", { name: "Numbered list" }),
    );
    expect(last()?.doc?.content[0].type).toBe("orderedList");
  });

  test("links the selection to a URL typed without its scheme, and refuses one that no link may lead to", async () => {
    const { textbox, last } = renderEditor({
      defaultValue: paragraph("harbour"),
    });
    await selectAll(textbox);
    await userEvent.click(screen.getByRole("button", { name: "Link" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a link" });
    const address = screen.getByRole("textbox", { name: "Address" });
    expect(dialog.contains(address)).toBe(true);
    await userEvent.type(address, "javascript:alert(1)");
    await userEvent.click(
      screen.getByRole("button", { name: "Save the link" }),
    );
    screen.getByRole("textbox", {
      name: "Address",
      description: /A link leads to a web address/,
    });
    expect(last()).toBeUndefined();
    await userEvent.clear(address);
    await userEvent.type(address, "café.example/harbour");
    await userEvent.click(
      screen.getByRole("button", { name: "Save the link" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(last()?.doc).toEqual({
      content: [
        {
          content: [
            {
              marks: [
                {
                  attrs: { href: "https://xn--caf-dma.example/harbour" },
                  type: "link",
                },
              ],
              text: "harbour",
              type: "text",
            },
          ],
          type: "paragraph",
        },
      ],
      type: "doc",
    });
    await waitFor(() => expect(document.activeElement).toBe(textbox));
  });

  test("saving a link submits no form of the page that holds the editor, and typing its URL changes none of the form's fields", async () => {
    const onSubmit = vi.fn((event: { preventDefault: () => void }) => {
      event.preventDefault();
    });
    const onFormChange = vi.fn();
    render(
      <I18nextProvider i18n={createAppI18n(["en"], document.documentElement)}>
        <form onSubmit={onSubmit} onChange={onFormChange}>
          <RichTextEditor label="Body" defaultValue={paragraph("harbour")} />
        </form>
      </I18nextProvider>,
    );
    await selectAll(screen.getByRole("textbox", { name: "Body" }));
    await userEvent.click(screen.getByRole("button", { name: "Link" }));
    await userEvent.type(
      await screen.findByRole("textbox", { name: "Address" }),
      "example.com{Enter}",
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(onSubmit).not.toHaveBeenCalled();
    expect(onFormChange).not.toHaveBeenCalled();
  });

  test("says that a text of more than 100,000 characters is too long to save, and gives the page why", async () => {
    const { textbox, last } = renderEditor({
      defaultValue: paragraph("a".repeat(MAX_CHARS)),
    });
    textbox.focus();
    await userEvent.paste("b");
    expect(last()?.error?.reason).toBe("too_large");
    expect(textbox.getAttribute("aria-invalid")).toBe("true");
    screen.getByRole("textbox", {
      name: "Body",
      description:
        "This text is too long to save: it may hold at most 100,000 characters.",
    });
  });

  test("says from the start that the text that it starts with is too long to save, and lets the user shorten it", () => {
    const { textbox } = renderEditor({
      defaultValue: paragraph("a".repeat(MAX_CHARS + 1)),
    });
    expect(textbox.getAttribute("contenteditable")).toBe("true");
    expect(textbox.getAttribute("aria-invalid")).toBe("true");
    screen.getByRole("textbox", {
      name: "Body",
      description:
        "This text is too long to save: it may hold at most 100,000 characters.",
    });
  });

  test("a text that its schema cannot read, as one of a later version of the app, cannot be changed, so that no change saves over it", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    try {
      const { textbox, changes } = renderEditor({
        defaultValue: {
          content: [
            {
              content: [
                { marks: [{ type: "underline" }], text: "Tide", type: "text" },
              ],
              type: "paragraph",
            },
          ],
          type: "doc",
        } as unknown as RichTextDoc,
      });
      expect(textbox.getAttribute("contenteditable")).toBe("false");
      // It holds nothing that the user should mend, so that a refusal of
      // the form takes the focus to the field that it is of instead.
      expect(textbox.getAttribute("aria-invalid")).toBeNull();
      screen.getByRole("textbox", {
        name: "Body",
        description: /this version of the app cannot show/,
      });
      expect(screen.queryByRole("toolbar")).toBeNull();
      textbox.focus();
      await userEvent.paste("x");
      expect(changes).toEqual([]);
    } finally {
      warn.mockRestore();
    }
  });

  test("a text that it cannot keep says so before the page's error, which marks it invalid, and which it says too", () => {
    const { textbox } = renderEditor({
      defaultValue: {
        content: [
          {
            attrs: { align: "center" },
            content: [{ text: "Tide", type: "text" }],
            type: "paragraph",
          },
        ],
        type: "doc",
      } as unknown as RichTextDoc,
      error: "The server refused it.",
    });
    expect(textbox.getAttribute("aria-invalid")).toBe("true");
    screen.getByRole("textbox", {
      name: "Body",
      description:
        /this version of the app cannot show.* The server refused it\.$/,
    });
  });

  test("a text too long to save that may not be changed says nothing of its length, and is not marked invalid, as the user cannot shorten it", () => {
    const { textbox } = renderEditor({
      defaultValue: paragraph("a".repeat(MAX_CHARS + 1)),
      editable: false,
    });
    expect(textbox.getAttribute("contenteditable")).toBe("false");
    expect(textbox.getAttribute("aria-invalid")).toBeNull();
    expect(textbox.getAttribute("aria-describedby")).toBeNull();
  });

  // A list of one item of text, nested within as many lists as depth.
  const nested = (depth: number, start = 1): object => ({
    ...(start === 1 ? {} : { attrs: { start } }),
    content: [
      {
        content: [
          paragraph("Tide").content[0],
          ...(depth > 1 ? [nested(depth - 1)] : []),
        ],
        type: "listItem",
      },
    ],
    type: "orderedList",
  });
  test.each([
    {
      what: "an attribute that this version does not have",
      content: [
        {
          attrs: { align: "center" },
          content: [{ text: "Tide", type: "text" }],
          type: "paragraph",
        },
      ],
    },
    {
      what: "a list numbered from further than this version allows",
      content: [nested(1, 5_000_000), paragraph("Shore").content[0]],
    },
    {
      what: "lists nested deeper than this version allows",
      content: [nested(6), paragraph("Shore").content[0]],
    },
    {
      what: "a link without a URL",
      content: [
        {
          content: [{ marks: [{ type: "link" }], text: "Tide", type: "text" }],
          type: "paragraph",
        },
      ],
    },
    {
      what: "a text too long, with an attribute that this version does not have after the text that takes it over the limit",
      content: [
        paragraph("a".repeat(MAX_CHARS + 1)).content[0],
        {
          attrs: { align: "center" },
          content: [{ text: "Tide", type: "text" }],
          type: "paragraph",
        },
      ],
    },
  ])(
    "a text that the server would refuse for anything but its size, as one of $what, cannot be changed either, so that no change elsewhere in it saves it changed",
    async ({ content }) => {
      const { textbox, changes } = renderEditor({
        defaultValue: { content, type: "doc" } as unknown as RichTextDoc,
      });
      expect(textbox.getAttribute("contenteditable")).toBe("false");
      screen.getByRole("textbox", {
        name: "Body",
        description: /this version of the app cannot show/,
      });
      textbox.focus();
      await userEvent.paste("x");
      expect(changes).toEqual([]);
    },
  );

  test("a text whose mention and value reference have IDs that are no strings shows them by what they are, without giving the page's names those IDs, and cannot be changed", () => {
    const names = {
      mention: vi.fn((id: string) => id.toUpperCase()),
      value: vi.fn((ref: string) => ref.toUpperCase()),
    };
    const { textbox } = renderEditor({
      defaultValue: {
        content: [
          {
            content: [
              { attrs: { id: 5 }, type: "mention" },
              { text: " has ", type: "text" },
              { attrs: { ref: null }, type: "valueRef" },
              { text: " and ", type: "text" },
              {
                attrs: { attribute: 7, ref: "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BVD" },
                type: "valueRef",
              },
            ],
            type: "paragraph",
          },
        ],
        type: "doc",
      } as unknown as RichTextDoc,
      names,
    });
    expect(textbox.textContent).toBe("@component has {value} and {value}");
    expect(names.mention).not.toHaveBeenCalled();
    expect(names.value).not.toHaveBeenCalled();
    expect(textbox.getAttribute("contenteditable")).toBe("false");
  });

  test("the page's own error goes before the editor's, and a text that may not be changed has no toolbar, and takes the focus, as from a click on its label", async () => {
    const { textbox } = renderEditor({
      defaultValue: paragraph("Read only"),
      editable: false,
      error: "The server refused it.",
    });
    expect(textbox.getAttribute("aria-readonly")).toBe("true");
    expect(textbox.getAttribute("contenteditable")).toBe("false");
    expect(textbox.getAttribute("aria-invalid")).toBe("true");
    screen.getByRole("textbox", {
      name: "Body",
      description: "The server refused it.",
    });
    expect(screen.queryByRole("toolbar")).toBeNull();
    expect(textbox.tabIndex).toBe(0);
    await userEvent.click(screen.getByText("Body"));
    await waitFor(() => expect(document.activeElement).toBe(textbox));
  });

  test("shows a mention and a value reference by the names that the page gives, and by what they are when it knows none", () => {
    const component = "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BVD";
    const variable = "var_01HQ8Z3XK4F7NAWR5TCQ9M2BVE";
    const { textbox } = renderEditor({
      defaultValue: {
        content: [
          {
            content: [
              { attrs: { id: component }, type: "mention" },
              { text: " has ", type: "text" },
              { attrs: { ref: variable }, type: "valueRef" },
              { text: " and ", type: "text" },
              {
                attrs: { ref: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BVF" },
                type: "valueRef",
              },
            ],
            type: "paragraph",
          },
        ],
        type: "doc",
      },
      names: {
        mention: (id) => (id === component ? "Elara" : undefined),
        value: (ref) => (ref === variable ? "gold" : undefined),
      },
    });
    expect(textbox.textContent).toBe("@Elara has {gold} and {value}");
  });

  test("shows a mention again by its name once the page gives it, as when the project's names have loaded", async () => {
    const component = "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BVD";
    const defaultValue: RichTextDoc = {
      content: [
        {
          content: [
            { text: "Ask ", type: "text" },
            { attrs: { id: component }, type: "mention" },
          ],
          type: "paragraph",
        },
      ],
      type: "doc",
    };
    const i18n = createAppI18n(["en"], document.documentElement);
    const { rerender } = render(
      <I18nextProvider i18n={i18n}>
        <RichTextEditor label="Body" defaultValue={defaultValue} />
      </I18nextProvider>,
    );
    const textbox = screen.getByRole("textbox", { name: "Body" });
    expect(textbox.textContent).toBe("Ask @component");
    rerender(
      <I18nextProvider i18n={i18n}>
        <RichTextEditor
          label="Body"
          defaultValue={defaultValue}
          names={{ mention: () => "Elara", value: () => undefined }}
        />
      </I18nextProvider>,
    );
    await waitFor(() => expect(textbox.textContent).toBe("Ask @Elara"));
  });

  test("a click on the label puts the focus in the text, as it does in every field", async () => {
    const { textbox } = renderEditor({ defaultValue: paragraph("harbour") });
    await userEvent.click(screen.getByText("Body"));
    await waitFor(() => expect(document.activeElement).toBe(textbox));
  });

  test("a text that may no longer be changed closes the link's dialog, which changes nothing", async () => {
    const onChange = vi.fn();
    const i18n = createAppI18n(["en"], document.documentElement);
    const editor = (editable: boolean) => (
      <I18nextProvider i18n={i18n}>
        <RichTextEditor
          label="Body"
          defaultValue={paragraph("harbour")}
          editable={editable}
          onChange={onChange}
        />
      </I18nextProvider>
    );
    const { rerender } = render(editor(true));
    await selectAll(screen.getByRole("textbox", { name: "Body" }));
    await userEvent.click(screen.getByRole("button", { name: "Link" }));
    await screen.findByRole("dialog", { name: "Add a link" });
    rerender(editor(false));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    rerender(editor(true));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });

  test("inserts what the user typed as the text of a link where nothing is selected", async () => {
    const { textbox, last } = renderEditor();
    textbox.focus();
    await userEvent.click(screen.getByRole("button", { name: "Link" }));
    await userEvent.type(
      await screen.findByRole("textbox", { name: "Address" }),
      "café.example",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Save the link" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(last()?.doc?.content[0]).toEqual({
      content: [
        {
          marks: [
            { attrs: { href: "https://xn--caf-dma.example" }, type: "link" },
          ],
          text: "café.example",
          type: "text",
        },
      ],
      type: "paragraph",
    });
  });

  test("what the user types right after a link inserted where nothing is selected is no part of the link", async () => {
    const { textbox, last } = renderEditor();
    textbox.focus();
    await userEvent.click(screen.getByRole("button", { name: "Link" }));
    await userEvent.type(
      await screen.findByRole("textbox", { name: "Address" }),
      "example.com{Enter}",
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(textbox));
    await userEvent.keyboard(", then");
    expect(last()?.doc?.content[0]).toEqual({
      content: [
        {
          marks: [{ attrs: { href: "https://example.com" }, type: "link" }],
          text: "example.com",
          type: "text",
        },
        { text: ", then", type: "text" },
      ],
      type: "paragraph",
    });
  });

  test("a form that holds the editor holds what it shows while the focus is in the link's dialog, though what it shows changed meanwhile, rather than show it anew without the dialog", async () => {
    function HeldForm({ entity }: { entity: { rev: number; body: string } }) {
      const held = useHeld(entity);
      return (
        <form key={held.key} {...held.props}>
          <RichTextEditor
            label="Body"
            defaultValue={paragraph(held.shown.body)}
            onChange={held.props.onChange}
          />
        </form>
      );
    }
    const i18n = createAppI18n(["en"], document.documentElement);
    const form = (rev: number, body: string) => (
      <I18nextProvider i18n={i18n}>
        <HeldForm entity={{ rev, body }} />
      </I18nextProvider>
    );
    const { rerender } = render(form(1, "harbour"));
    const textbox = screen.getByRole("textbox", { name: "Body" });
    await selectAll(textbox);
    // Another session changes the text while the user is in it.
    rerender(form(2, "shore"));
    await userEvent.click(screen.getByRole("button", { name: "Link" }));
    await screen.findByRole("dialog", { name: "Add a link" });
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(screen.getByRole("dialog", { name: "Add a link" })).toBeTruthy();
    expect(textbox.isConnected).toBe(true);
    expect(textbox.textContent).toBe("harbour");
    // The focus leaving the form, unchanged, lets go of it.
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(document.activeElement).toBe(textbox));
    textbox.blur();
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Body" }).textContent).toBe(
        "shore",
      ),
    );
  });

  test("a form that holds the editor, shown anew while the focus is in the text, as once it is saved, gives the focus back to the text", async () => {
    let release = () => {};
    function HeldForm({ entity }: { entity: { rev: number; body: string } }) {
      const held = useHeld(entity);
      release = held.release;
      return (
        <form key={held.key} {...held.props}>
          <input aria-label="Title" />
          <RichTextEditor
            label="Body"
            defaultValue={paragraph(held.shown.body)}
            onChange={held.props.onChange}
          />
        </form>
      );
    }
    const i18n = createAppI18n(["en"], document.documentElement);
    const form = (rev: number, body: string) => (
      <I18nextProvider i18n={i18n}>
        <HeldForm entity={{ rev, body }} />
      </I18nextProvider>
    );
    const { rerender } = render(form(1, "harbour"));
    const textbox = screen.getByRole("textbox", { name: "Body" });
    textbox.focus();
    rerender(form(2, "shore"));
    expect(textbox.textContent).toBe("harbour");
    act(() => release());
    const shown = screen.getByRole("textbox", { name: "Body" });
    expect(shown).not.toBe(textbox);
    expect(shown.textContent).toBe("shore");
    expect(document.activeElement).toBe(shown);
  });

  test("the text of a link inserted where nothing is selected has the marks of text typed there", async () => {
    const { textbox, last } = renderEditor();
    textbox.focus();
    await userEvent.click(screen.getByRole("button", { name: "Bold" }));
    await userEvent.click(screen.getByRole("button", { name: "Link" }));
    await userEvent.type(
      await screen.findByRole("textbox", { name: "Address" }),
      "example.com{Enter}",
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(last()?.doc?.content[0]).toEqual({
      content: [
        {
          marks: [
            { type: "bold" },
            { attrs: { href: "https://example.com" }, type: "link" },
          ],
          text: "example.com",
          type: "text",
        },
      ],
      type: "paragraph",
    });
  });
});
