// What a paste asks before it pastes nodes that name what the board's
// project lacks (PRD FR-EDIT-07 AC1, plan note N71): the server's report of
// what of the clipboard a paste onto the board would resolve to nothing, as
// pasted into another project, or onto another board, whose variables the
// expressions name. The dialog lists the variables, the components, and the
// attribute definitions that the paste can create first, those of a type
// that it creates with a component among them (typesCreated), as all that
// Create and paste creates (createCommands), and says how many
// speakers, nodes, and cover images it leaves out, which it cannot; the user
// pastes with what is missing created, as the report gives it, or without,
// whose names then name nothing, which the validation reports, or cancels,
// which sends nothing.

import type {
  ClipboardReport,
  ClipboardVariable,
  MissingComponent,
} from "@storycreep/shared/api";
import { Dialog } from "radix-ui";
import { useTranslation } from "react-i18next";
import { typeName } from "../content/ValueField";
import { Button } from "../ui/Button";
import { closesBy, DialogFrame, focusBackTo, type Opener } from "../ui/Dialog";
import { creatable, typesCreated } from "./clipboard";

type TFunction = ReturnType<typeof useTranslation>["t"];

/** How many of each list the dialog names, of a report of thousands, before it says how many more there are. */
const listed = 20;

/** Returns how the dialog names variable: its key, its scope, and its type. */
function variableName(t: TFunction, variable: ClipboardVariable): string {
  return variable.scope === "global"
    ? t("paste.variable_global", {
        key: variable.key,
        type: typeName(t, variable.type),
      })
    : t("paste.variable_board", {
        key: variable.key,
        type: typeName(t, variable.type),
      });
}

/** Returns how the dialog names the component of missing: its name and key, and the type that it is created with, where the project lacks that too. */
function componentName(t: TFunction, missing: MissingComponent): string {
  const { component } = missing;
  return missing.component_type_id === undefined
    ? t("paste.component_new_type", {
        name: component.name,
        key: component.key,
        type: component.type.name,
      })
    : t("paste.component", { name: component.name, key: component.key });
}

/** A list of what the paste creates, of at most listed items, and how many more it has. */
function Listed({ heading, items }: { heading: string; items: string[] }) {
  const { t } = useTranslation();
  if (items.length === 0) {
    return null;
  }
  return (
    <section className="space-y-1">
      <h3 className="font-semibold">{heading}</h3>
      <ul className="list-disc space-y-1 ps-6 break-words">
        {items.slice(0, listed).map((item, i) => (
          // The items are as the report lists them, and may repeat a name.
          // biome-ignore lint/suspicious/noArrayIndexKey: the list does not change while it is shown.
          <li key={i}>{item}</li>
        ))}
      </ul>
      {items.length > listed && (
        <p className="text-fg-muted">
          {t("paste.more", { count: items.length - listed })}
        </p>
      )}
    </section>
  );
}

/**
 * The dialog of a paste whose report lists what the board's project lacks:
 * onPaste pastes, creating what the report lists first where create says
 * so, and onCancel closes it; the focus goes back to opener as it closes.
 */
export function PasteDialog({
  report,
  onPaste,
  onCancel,
  opener,
}: {
  report: ClipboardReport;
  onPaste: (create: boolean) => void;
  onCancel: () => void;
  opener: Opener;
}) {
  const { t } = useTranslation();
  const creates = creatable(report);
  return (
    <Dialog.Root open onOpenChange={closesBy(onCancel)}>
      <DialogFrame
        title={t("paste.title")}
        description={
          creates ? t("paste.intro_create") : t("paste.intro_left_out")
        }
        onCloseAutoFocus={focusBackTo(opener)}
      >
        <Listed
          heading={t("paste.variables")}
          items={report.variables.map((v) => variableName(t, v))}
        />
        <Listed
          heading={t("paste.components")}
          items={report.components.map((c) => componentName(t, c))}
        />
        <Listed
          heading={t("paste.attributes")}
          items={[
            ...typesCreated(report).flatMap((made) => made.attributes),
            ...report.attributes.map((a) => a.attribute),
          ].map((a) => t("paste.attribute", { name: a.name, key: a.key }))}
        />
        {(report.speakers.length > 0 ||
          report.nodes.length > 0 ||
          report.assets.length > 0) && (
          <ul className="list-disc space-y-1 ps-6">
            {report.speakers.length > 0 && (
              <li>
                {t("paste.speakers_left_out", {
                  count: report.speakers.length,
                })}
              </li>
            )}
            {report.nodes.length > 0 && (
              <li>
                {t("paste.nodes_left_out", { count: report.nodes.length })}
              </li>
            )}
            {report.assets.length > 0 && (
              <li>
                {t("paste.assets_left_out", { count: report.assets.length })}
              </li>
            )}
          </ul>
        )}
        {creates && <p className="text-fg-muted">{t("paste.without_hint")}</p>}
        <div className="flex flex-wrap justify-end gap-2">
          <Dialog.Close asChild>
            <Button>{t("paste.cancel")}</Button>
          </Dialog.Close>
          {creates && (
            <Button onClick={() => onPaste(false)}>{t("paste.without")}</Button>
          )}
          <Button variant="primary" onClick={() => onPaste(creates)}>
            {creates ? t("paste.create") : t("paste.paste")}
          </Button>
        </div>
      </DialogFrame>
    </Dialog.Root>
  );
}
