// The editor of the rich text of a project's content (PRD FR-TEXT-01,
// ADR-020): node bodies, connection labels, component descriptions, and
// comments use it. It is TipTap's editor of the schema of
// schema/rich-text/README.md (extensions.ts), which keeps its document one
// that the server stores (normalize.ts), cleans what is pasted into it to
// what the schema holds (paste.ts), and gives the page its document in its
// canonical form, or says why the server would refuse it, as a text that is
// too large (convert.ts). It is labelled as a field of the app's forms, has
// a toolbar of its marks and lists, whose buttons are at least 44 pixels
// square (PRD FR-EDIT-09), and edits a link's URL in a dialog.

import type { RichTextDoc } from "@storycreep/shared/api";
import {
  emptyRichText,
  MAX_CHARS,
  type RichTextError,
} from "@storycreep/shared/richtext";
import {
  type Editor,
  EditorContent,
  useEditor,
  useEditorState,
} from "@tiptap/react";
import { Dialog, Toolbar } from "radix-ui";
import {
  type FormEvent,
  type ReactNode,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/Button";
import { DialogFrame } from "../ui/Dialog";
import { TextField } from "../ui/Field";
import { type RichTextValue, storedOf } from "./convert";
import {
  pasteProps,
  type RichTextNames,
  relabel,
  richTextExtensions,
} from "./extensions";
import { typedHref } from "./link";

export interface RichTextEditorProps {
  /** The field's label, which names the text for a screen reader too. */
  label: string;
  hint?: string;
  /** What the page says is wrong with the text, as the server's refusal of it. */
  error?: string;
  /**
   * The document that the editor starts with, the empty one when it is not
   * given. The editor keeps what the user changes from then on, so a page
   * that shows another document gives the editor a key of its own.
   */
  defaultValue?: RichTextDoc;
  /** Called after each change with the document as the server stores it, or with why the server would refuse it. */
  onChange?: (value: RichTextValue) => void;
  /** Whether the user may change the text, as by default; otherwise it is shown, to be read, selected, and copied. */
  editable?: boolean;
  /** What the empty editor shows. */
  placeholder?: string;
  /**
   * What the text's mentions and value references show of what they refer
   * to. The editor shows them again, walking the whole text, whenever it is
   * another object, so a page keeps the same one until the names change, as
   * useMemo keeps it.
   */
  names?: RichTextNames;
}

/** Returns the message of why the server would refuse a document, in the page's locale. */
function messageOf(
  t: ReturnType<typeof useTranslation>["t"],
  error: RichTextError,
): string {
  if (error.reason === "too_large") {
    return error.limit === "chars"
      ? t("richtext.error.too_many_chars", { max: MAX_CHARS })
      : t("richtext.error.too_large");
  }
  return t("richtext.error.invalid");
}

export function RichTextEditor({
  label,
  hint,
  error,
  defaultValue,
  onChange,
  editable = true,
  placeholder,
  names,
}: RichTextEditorProps) {
  const { t } = useTranslation();
  const id = useId();
  const labelId = `${id}-label`;
  const hintId = hint === undefined ? undefined : `${id}-hint`;
  // Why the server would refuse what the editor holds, which the page's own
  // error goes before.
  const [refused, setRefused] = useState<RichTextError>();
  const shownError =
    error ?? (refused === undefined ? undefined : messageOf(t, refused));
  const errorId = shownError === undefined ? undefined : `${id}-error`;
  const describedBy =
    [hintId, errorId].filter((s) => s !== undefined).join(" ") || undefined;
  const [linking, setLinking] = useState(false);
  // What the editor's callbacks, made once with it, read as the page renders.
  const unknownMention = t("richtext.unknown_mention");
  const unknownValue = t("richtext.unknown_value");
  const current = {
    onChange,
    names,
    unknown: { mention: unknownMention, value: unknownValue },
    placeholder,
  };
  const latest = useRef(current);
  useEffect(() => {
    latest.current = current;
  });
  const [initial] = useState(() => ({
    content: defaultValue ?? emptyRichText(),
    extensions: richTextExtensions({
      labels: {
        mention: (ref) =>
          latest.current.names?.mention(ref) ?? latest.current.unknown.mention,
        value: (ref, attribute) =>
          latest.current.names?.value(ref, attribute) ??
          latest.current.unknown.value,
      },
      placeholder: () => latest.current.placeholder ?? "",
      onLinkShortcut: () => setLinking(true),
    }),
  }));
  const editorProps = useMemo(
    () => ({
      ...pasteProps,
      attributes: {
        role: "textbox",
        "aria-multiline": "true",
        "aria-labelledby": labelId,
        ...(describedBy === undefined
          ? {}
          : { "aria-describedby": describedBy }),
        ...(shownError === undefined ? {} : { "aria-invalid": "true" }),
        ...(editable ? {} : { "aria-readonly": "true" }),
        class: "rich-text min-h-24 rounded-b-md px-3 py-2 text-fg break-words",
      },
    }),
    [labelId, describedBy, shownError, editable],
  );
  const editor = useEditor({
    content: initial.content,
    extensions: initial.extensions,
    editable,
    // The editor's styles are the app's stylesheet's (styles.css), since the
    // page's Content-Security-Policy refuses a style element without the
    // response's nonce.
    injectCSS: false,
    editorProps,
    onUpdate: ({ editor }) => {
      const value = storedOf(editor.state.doc);
      // A refusal that says what the one before it said is no change of the
      // editor's, which would render it again on each keystroke.
      setRefused((before) =>
        before !== undefined &&
        value.error?.reason === before.reason &&
        value.error.limit === before.limit
          ? before
          : value.error,
      );
      latest.current.onChange?.(value);
    },
  });
  // Whether the user may change the text is no change of it.
  useEffect(() => {
    if (editor !== null && editor.isEditable !== editable) {
      editor.setEditable(editable, false);
    }
  }, [editor, editable]);
  // The editor draws a mention, a value reference, and the placeholder once,
  // and again as what names them changes, as when the project's names load.
  // biome-ignore lint/correctness/useExhaustiveDependencies: these are what the editor shows, which it reads from latest.
  useEffect(() => {
    if (editor !== null) {
      relabel(editor);
    }
  }, [editor, names, unknownMention, unknownValue, placeholder]);
  return (
    <div className="flex flex-col gap-1">
      <div id={labelId} className="font-medium">
        {label}
      </div>
      {hint !== undefined && (
        <p id={hintId} className="text-sm text-fg-muted">
          {hint}
        </p>
      )}
      <div
        className={`rounded-md border bg-canvas ${shownError === undefined ? "border-border" : "border-danger"}`}
      >
        {editable && editor !== null && (
          <FormatBar
            editor={editor}
            label={label}
            onLink={() => setLinking(true)}
          />
        )}
        <EditorContent editor={editor} />
      </div>
      {shownError !== undefined && (
        <p id={errorId} className="text-sm text-danger">
          {shownError}
        </p>
      )}
      {linking && editor !== null && (
        <LinkDialog editor={editor} onClose={() => setLinking(false)} />
      )}
    </div>
  );
}

/** A button of the toolbar, which keeps the editor's focus and selection as it is pressed. */
function FormatButton({
  label,
  shortcut,
  pressed,
  disabled,
  onPress,
  children,
}: {
  label: string;
  /** The keys that do the same, as aria-keyshortcuts writes them. */
  shortcut: string;
  pressed?: boolean;
  disabled?: boolean;
  onPress: () => void;
  children: ReactNode;
}) {
  return (
    <Toolbar.Button
      type="button"
      aria-label={label}
      aria-pressed={pressed}
      aria-keyshortcuts={shortcut}
      title={label}
      disabled={disabled}
      // A press with the pointer leaves the focus in the text, so that the
      // selection that the button changes stays, and so does the on-screen
      // keyboard.
      onMouseDown={(event) => event.preventDefault()}
      onClick={onPress}
      className="inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center rounded text-fg hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-60 aria-pressed:bg-accent aria-pressed:text-accent-fg aria-pressed:hover:bg-accent-hover"
    >
      <svg
        aria-hidden="true"
        viewBox="0 0 24 24"
        className="size-5"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {children}
      </svg>
    </Toolbar.Button>
  );
}

/** Returns the modifier of the editor's shortcuts as aria-keyshortcuts names it: Meta on Apple's systems, and Control elsewhere. */
function modifier(): string {
  return /Mac|iPhone|iPad/.test(navigator.platform) ? "Meta" : "Control";
}

/** The editor's toolbar: its marks, its link, and its lists, each pressed where the selection has it. */
function FormatBar({
  editor,
  label,
  onLink,
}: {
  editor: Editor;
  label: string;
  onLink: () => void;
}) {
  const { t } = useTranslation();
  const mod = modifier();
  const state = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e.isActive("bold"),
      italic: e.isActive("italic"),
      strike: e.isActive("strike"),
      code: e.isActive("code"),
      link: e.isActive("link"),
      bulletList: e.isActive("bulletList"),
      orderedList: e.isActive("orderedList"),
      canBold: e.can().toggleBold(),
      canItalic: e.can().toggleItalic(),
      canStrike: e.can().toggleStrike(),
      canCode: e.can().toggleCode(),
    }),
  });
  const chain = () => editor.chain().focus();
  return (
    <Toolbar.Root
      aria-label={t("richtext.toolbar", { field: label })}
      className="flex flex-wrap gap-1 border-b border-border p-1"
    >
      <FormatButton
        label={t("richtext.bold")}
        shortcut={`${mod}+B`}
        pressed={state.bold}
        disabled={!state.canBold}
        onPress={() => chain().toggleBold().run()}
      >
        <path d="M7 5h6a3.5 3.5 0 0 1 0 7H7zM7 12h7a3.5 3.5 0 0 1 0 7H7z" />
      </FormatButton>
      <FormatButton
        label={t("richtext.italic")}
        shortcut={`${mod}+I`}
        pressed={state.italic}
        disabled={!state.canItalic}
        onPress={() => chain().toggleItalic().run()}
      >
        <path d="M14 5h5M5 19h5M15 5 9 19" />
      </FormatButton>
      <FormatButton
        label={t("richtext.strike")}
        shortcut={`${mod}+Shift+S`}
        pressed={state.strike}
        disabled={!state.canStrike}
        onPress={() => chain().toggleStrike().run()}
      >
        <path d="M4 12h16M16 7.5C15.4 6 13.9 5 12 5c-2.5 0-4 1.3-4 3 0 1.4 1 2.3 2.6 2.9M8 16.5c.6 1.5 2.1 2.5 4 2.5 2.5 0 4-1.3 4-3 0-.6-.2-1.1-.5-1.5" />
      </FormatButton>
      <FormatButton
        label={t("richtext.code")}
        shortcut={`${mod}+E`}
        pressed={state.code}
        disabled={!state.canCode}
        onPress={() => chain().toggleCode().run()}
      >
        <path d="m9 8-4 4 4 4M15 8l4 4-4 4" />
      </FormatButton>
      <FormatButton
        label={t("richtext.link")}
        shortcut={`${mod}+K`}
        pressed={state.link}
        disabled={state.code}
        onPress={onLink}
      >
        <path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1" />
      </FormatButton>
      <Toolbar.Separator className="mx-1 w-px self-stretch bg-border" />
      <FormatButton
        label={t("richtext.bullet_list")}
        shortcut={`${mod}+Shift+8`}
        pressed={state.bulletList}
        onPress={() => chain().toggleBulletList().run()}
      >
        <path d="M10 7h10M10 12h10M10 17h10M5 7h.01M5 12h.01M5 17h.01" />
      </FormatButton>
      <FormatButton
        label={t("richtext.ordered_list")}
        shortcut={`${mod}+Shift+7`}
        pressed={state.orderedList}
        onPress={() => chain().toggleOrderedList().run()}
      >
        <path d="M11 7h9M11 12h9M11 17h9M4 5h1.5v4M4 9h3M4 13.5a1.5 1.5 0 0 1 3 0c0 1.2-3 1.8-3 3.5h3" />
      </FormatButton>
    </Toolbar.Root>
  );
}

/**
 * The dialog that edits the link of the selection, or of the link that the
 * caret is in, whose URL it shows: it links the selection, or inserts what
 * the user typed as linked text where nothing is selected, to a URL that a
 * link may lead to, as one typed without a scheme, example.com, is an https
 * URL (typedHref), and says so of one that no link may; and it removes a
 * link. Closed, it gives the focus back to the text.
 */
function LinkDialog({
  editor,
  onClose,
}: {
  editor: Editor;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [current] = useState(
    () => editor.getAttributes("link").href as string | undefined,
  );
  const [invalid, setInvalid] = useState(false);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const typed = String(new FormData(event.currentTarget).get("href"));
    const href = typedHref(typed);
    if (href === undefined) {
      setInvalid(true);
      return;
    }
    const chain = editor.chain().focus();
    if (editor.state.selection.empty && !editor.isActive("link")) {
      // The text is what the user typed, as café.example, rather than the
      // URL as the link stores it, https://xn--caf-dma.example.
      chain
        .insertContent({
          type: "text",
          text: typed.trim(),
          marks: [{ type: "link", attrs: { href } }],
        })
        .run();
    } else {
      chain.extendMarkRange("link").setLink({ href }).run();
    }
    onClose();
  };
  const remove = () => {
    editor.chain().focus().extendMarkRange("link").unsetLink().run();
    onClose();
  };
  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <DialogFrame
        title={
          current === undefined
            ? t("richtext.link_add")
            : t("richtext.link_edit")
        }
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          // The page may have taken the editor away as the dialog closed.
          if (!editor.isDestroyed) {
            editor.commands.focus();
          }
        }}
      >
        <form className="space-y-4" onSubmit={submit} noValidate>
          <TextField
            label={t("richtext.link_url")}
            hint={t("richtext.link_hint")}
            name="href"
            type="url"
            inputMode="url"
            autoComplete="url"
            spellCheck={false}
            defaultValue={current}
            error={invalid ? t("richtext.link_invalid") : undefined}
            autoFocus
          />
          <div className="flex flex-wrap gap-2">
            <Button type="submit" variant="primary">
              {t("richtext.link_save")}
            </Button>
            {current !== undefined && (
              <Button onClick={remove}>{t("richtext.link_remove")}</Button>
            )}
            <Dialog.Close asChild>
              <Button>{t("richtext.link_cancel")}</Button>
            </Dialog.Close>
          </div>
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}
