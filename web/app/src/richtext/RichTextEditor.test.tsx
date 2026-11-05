import type { RichTextDoc } from "@storycreep/shared/api";
import { MAX_CHARS } from "@storycreep/shared/richtext";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { describe, expect, test, vi } from "vitest";
import { createAppI18n } from "../i18n";
import "../test/editor";
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

  test("the page's own error goes before the editor's, and a text that may not be changed has no toolbar", () => {
    const { textbox } = renderEditor({
      defaultValue: paragraph("Read only"),
      editable: false,
      error: "The server refused it.",
    });
    expect(textbox.getAttribute("aria-readonly")).toBe("true");
    expect(textbox.getAttribute("contenteditable")).toBe("false");
    screen.getByRole("textbox", {
      name: "Body",
      description: "The server refused it.",
    });
    expect(screen.queryByRole("toolbar")).toBeNull();
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
