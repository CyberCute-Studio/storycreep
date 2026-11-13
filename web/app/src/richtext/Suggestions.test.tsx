import type { RichTextDoc } from "@storycreep/shared/api";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Editor } from "@tiptap/core";
import { I18nextProvider } from "react-i18next";
import { describe, expect, test, vi } from "vitest";
import { createAppI18n } from "../i18n";
import "../test/editor";
import type { RichTextChoices } from "./choices";
import type { RichTextValue } from "./convert";
import { RichTextEditor, type RichTextEditorProps } from "./RichTextEditor";

// The IDs of what the text refers to, as the server writes them.
const goldId = "var_01HQ8Z3XK4F7NAWR5TCQ9M2BV1";
const bigGoldId = "var_01HQ8Z3XK4F7NAWR5TCQ9M2BV2";
const elaraId = "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BV3";
const bramId = "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BV4";
const trustId = "atr_01HQ8Z3XK4F7NAWR5TCQ9M2BV5";

const choices: RichTextChoices = {
  values: [
    { ref: bigGoldId, name: "big_gold", detail: "Global variable" },
    {
      ref: elaraId,
      attribute: trustId,
      name: "elara.trust",
      detail: "Trust of Elara",
      terms: ["Elara Trust"],
    },
    { ref: goldId, name: "gold", detail: "Global variable" },
  ],
  mentions: [
    { id: bramId, name: "Bram", detail: "Character" },
    { id: elaraId, name: "Elara", detail: "Character" },
  ],
};

const names = {
  mention: (id: string) =>
    choices.mentions.find((m) => m.id === id)?.name ?? undefined,
  value: (ref: string, attribute: string | undefined) =>
    choices.values.find((v) => v.ref === ref && v.attribute === attribute)
      ?.name,
};

/** Renders the editor of props in English, with the choices and names above, and returns what each change gave the page. */
function renderEditor(props: Partial<RichTextEditorProps> = {}) {
  const changes: RichTextValue[] = [];
  const onChange = vi.fn((value: RichTextValue) => {
    changes.push(value);
  });
  render(
    <I18nextProvider i18n={createAppI18n(["en"], document.documentElement)}>
      <RichTextEditor
        label="Body"
        onChange={onChange}
        names={names}
        choices={choices}
        {...props}
      />
    </I18nextProvider>,
  );
  const textbox = screen.getByRole("textbox", { name: "Body" });
  return { textbox, last: () => changes.at(-1) };
}

/** Returns the list of what { offers, once it shows. */
function valueList() {
  return screen.findByRole("listbox", { name: "Values to show" });
}

/** Returns the names of what list offers, as it shows them, and which is highlighted. */
function offersIn(list: HTMLElement) {
  return within(list)
    .getAllByRole("option")
    .map((option) => {
      const shown = option.firstElementChild?.textContent ?? "";
      return option.getAttribute("aria-selected") === "true"
        ? `[${shown}]`
        : shown;
    });
}

const paragraphOf = (
  ...content: NonNullable<
    Extract<RichTextDoc["content"][number], { type: "paragraph" }>["content"]
  >
): RichTextDoc => ({ content: [{ content, type: "paragraph" }], type: "doc" });

describe("the { and @ of the rich-text editor", () => {
  test("{ offers the values that what is typed after it names, which the arrows move through and Enter inserts as a reference to its ID", async () => {
    const { textbox, last } = renderEditor();
    textbox.focus();
    await userEvent.keyboard("You have {{go");
    const list = await valueList();
    expect(offersIn(list)).toEqual(["[{gold}]", "{big_gold}"]);
    // The text names the list and the offer highlighted, and keeps the
    // focus, as a combobox does.
    expect(textbox.getAttribute("aria-autocomplete")).toBe("list");
    expect(textbox.getAttribute("aria-controls")).toBe(list.id);
    const [gold, bigGold] = within(list).getAllByRole("option");
    expect(textbox.getAttribute("aria-activedescendant")).toBe(gold.id);
    await userEvent.keyboard("{ArrowDown}");
    await waitFor(() =>
      expect(offersIn(list)).toEqual(["{gold}", "[{big_gold}]"]),
    );
    expect(textbox.getAttribute("aria-activedescendant")).toBe(bigGold.id);
    await userEvent.keyboard("{ArrowDown}{ArrowUp}");
    await waitFor(() =>
      expect(offersIn(list)).toEqual(["{gold}", "[{big_gold}]"]),
    );
    await userEvent.keyboard("{ArrowUp}{Enter}");
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(document.activeElement).toBe(textbox);
    expect(textbox.hasAttribute("aria-activedescendant")).toBe(false);
    expect(textbox.hasAttribute("aria-controls")).toBe(false);
    // The reference stores the variable's ID, and shows its name.
    expect(last()?.doc).toEqual(
      paragraphOf(
        { text: "You have ", type: "text" },
        { attrs: { ref: goldId }, type: "valueRef" },
      ),
    );
    expect(textbox.textContent).toBe("You have {gold}");
    // What is typed next follows the reference.
    await userEvent.keyboard(" coins");
    expect(last()?.doc?.content[0]).toEqual({
      content: [
        { text: "You have ", type: "text" },
        { attrs: { ref: goldId }, type: "valueRef" },
        { text: " coins", type: "text" },
      ],
      type: "paragraph",
    });
  });

  test("inserts a component's attribute by its component's and its own IDs, with the digits after the point typed after a colon, which its chip shows", async () => {
    const { textbox, last } = renderEditor();
    textbox.focus();
    await userEvent.keyboard("{{trust:2");
    const list = await valueList();
    expect(offersIn(list)).toEqual(["[{elara.trust:2}]"]);
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(last()?.doc).toEqual(
      paragraphOf({
        attrs: { attribute: trustId, decimals: 2, ref: elaraId },
        type: "valueRef",
      }),
    );
    expect(textbox.textContent).toBe("{elara.trust:2}");
  });

  test("@ offers the components, and a tap on one inserts its mention, with the marks of the text typed there, keeping the focus in the text", async () => {
    const { textbox, last } = renderEditor();
    textbox.focus();
    await userEvent.click(screen.getByRole("button", { name: "Bold" }));
    await userEvent.keyboard("Ask @el");
    const list = await screen.findByRole("listbox", {
      name: "Components to mention",
    });
    expect(offersIn(list)).toEqual(["[@Elara]"]);
    await userEvent.click(within(list).getByRole("option"));
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(document.activeElement).toBe(textbox);
    expect(last()?.doc).toEqual(
      paragraphOf(
        { marks: [{ type: "bold" }], text: "Ask ", type: "text" },
        { attrs: { id: elaraId }, marks: [{ type: "bold" }], type: "mention" },
      ),
    );
  });

  test("Escape closes the list and keeps what was typed, after which the brace offers nothing until the caret has left it", async () => {
    const { textbox, last } = renderEditor();
    textbox.focus();
    await userEvent.keyboard("{{go");
    await valueList();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    await userEvent.keyboard("ld");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(last()?.doc).toEqual(paragraphOf({ text: "{gold", type: "text" }));
    // Another brace offers again.
    await userEvent.keyboard(" {{");
    expect(offersIn(await valueList())).toEqual([
      "[{big_gold}]",
      "{elara.trust}",
      "{gold}",
    ]);
  });

  test("offers nothing for the @ of an email address, nor in code, nor where the page gives no choices, and says when nothing matches", async () => {
    const { textbox } = renderEditor();
    textbox.focus();
    await userEvent.keyboard("keeper@ex");
    expect(screen.queryByRole("listbox")).toBeNull();
    await userEvent.keyboard(" {{zz");
    expect(
      await screen.findByText("No value matches what you typed."),
    ).toBeDefined();
    expect(screen.queryByRole("listbox")).toBeNull();
    // Enter, with nothing to choose, ends the paragraph.
    await userEvent.keyboard("{Enter}");
    expect(screen.queryByText("No value matches what you typed.")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Code" }));
    await userEvent.keyboard("{{go");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  test("a text whose page gives it nothing to offer, or that may not be changed, takes { and @ as any other character", async () => {
    const { textbox, last } = renderEditor({ choices: undefined });
    textbox.focus();
    await userEvent.keyboard("{{go @el");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(textbox.hasAttribute("aria-autocomplete")).toBe(false);
    expect(last()?.doc).toEqual(paragraphOf({ text: "{go @el", type: "text" }));
  });

  test("closes the list while the text does not have the focus, and shows it again once it has", async () => {
    renderEditor();
    const textbox = screen.getByRole("textbox", { name: "Body" });
    textbox.focus();
    await userEvent.keyboard("{{go");
    await valueList();
    act(() => textbox.blur());
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    act(() => textbox.focus());
    await valueList();
  });

  test("the toolbar sets the digits after the point of the value reference selected, which its chip shows", async () => {
    const { textbox, last } = renderEditor({
      defaultValue: paragraphOf(
        { text: "Gold: ", type: "text" },
        { attrs: { ref: goldId }, type: "valueRef" },
      ),
    });
    const format = screen.getByRole("button", { name: "Number format" });
    expect(format).toHaveProperty("disabled", true);
    const editor = (textbox as unknown as { editor: Editor }).editor;
    act(() => {
      editor.commands.focus();
      editor.commands.setNodeSelection(7);
    });
    await waitFor(() => expect(format).toHaveProperty("disabled", false));
    await userEvent.click(format);
    const dialog = await screen.findByRole("dialog", {
      name: "Number format of the value",
    });
    const shown = within(dialog).getByRole("combobox", {
      name: "Show a number with",
    });
    expect((shown as HTMLSelectElement).value).toBe("");
    await userEvent.selectOptions(shown, "2 digits after the point");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Save the format" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(last()?.doc?.content[0]).toEqual({
      content: [
        { text: "Gold: ", type: "text" },
        { attrs: { decimals: 2, ref: goldId }, type: "valueRef" },
      ],
      type: "paragraph",
    });
    expect(textbox.textContent).toBe("Gold: {gold:2}");
    // The text has the focus again, with the reference selected.
    await waitFor(() => expect(document.activeElement).toBe(textbox));
    expect(format).toHaveProperty("disabled", false);
    // As the value is, again.
    await userEvent.click(format);
    await userEvent.selectOptions(
      within(
        await screen.findByRole("dialog", {
          name: "Number format of the value",
        }),
      ).getByRole("combobox"),
      "As the value is",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Save the format" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(textbox.textContent).toBe("Gold: {gold}");
    expect(last()?.doc?.content[0]).toEqual({
      content: [
        { text: "Gold: ", type: "text" },
        { attrs: { ref: goldId }, type: "valueRef" },
      ],
      type: "paragraph",
    });
  });
});
