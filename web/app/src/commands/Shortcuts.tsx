// The table of the shortcuts (PRD FR-EDIT-10, plan note N72), a dialog that
// ? on the canvas, the palette, and the canvas's Keyboard shortcuts open:
// every command that has keys, in the groups that FR-EDIT-10 names, by the
// same IDs as the palette's, each with its keys on the page's platform and
// where they act, anywhere on the page, outside the fields that the user
// types in, or on the canvas.

import { Dialog } from "radix-ui";
import { type RefObject, useId } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/Button";
import { DialogFrame, focusBackTo } from "../ui/Dialog";
import {
  type CommandId,
  type CommandSpec,
  commandIds,
  commandName,
  commandOf,
  type Group,
  groupName,
  groups,
} from "./catalog";
import { KeyCaps } from "./KeyCaps";
import { bindingOn } from "./keys";
import { usePlatform } from "./registry";

/**
 * The dialog of the table of the shortcuts, open while open says so, which
 * gives the focus back to opener, what had it as the table was asked for,
 * or to the page's content where that has gone, as it closes, since a key
 * opens it, and no Dialog.Trigger of Radix's.
 */
export function Shortcuts({
  open,
  opener,
  onOpenChange,
}: {
  open: boolean;
  opener: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      {open && (
        <DialogFrame
          title={t("shortcuts.title")}
          description={t("shortcuts.description")}
          onCloseAutoFocus={focusBackTo(() => opener.current, {
            preventScroll: true,
          })}
        >
          <ShortcutTable />
          <p className="text-fg-muted">{t("shortcuts.space")}</p>
          <div className="flex justify-end">
            <Dialog.Close asChild>
              <Button>{t("shortcuts.close")}</Button>
            </Dialog.Close>
          </div>
        </DialogFrame>
      )}
    </Dialog.Root>
  );
}

/** Says where the keys of spec act. */
function whereOf(
  t: ReturnType<typeof useTranslation>["t"],
  spec: CommandSpec,
): string {
  if (spec.on === "canvas") {
    return t("shortcuts.on_canvas");
  }
  return spec.outsideText === true
    ? t("shortcuts.outside_text")
    : t("shortcuts.anywhere");
}

/** The table, a section of each group that has a command with keys on the page's platform. */
export function ShortcutTable() {
  const platform = usePlatform();
  const keyed = commandIds.filter((id) =>
    commandOf(id).keys.some((b) => bindingOn(b, platform)),
  );
  return (
    <div className="space-y-4">
      {groups.map((group) => {
        const ids = keyed.filter((id) => commandOf(id).group === group);
        return ids.length === 0 ? null : (
          <ShortcutGroup key={group} group={group} ids={ids} />
        );
      })}
    </div>
  );
}

function ShortcutGroup({ group, ids }: { group: Group; ids: CommandId[] }) {
  const { t } = useTranslation();
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className="space-y-1">
      <h3 id={headingId} className="font-semibold">
        {groupName(t, group)}
      </h3>
      <table className="w-full text-start" aria-labelledby={headingId}>
        <thead className="sr-only">
          <tr>
            <th scope="col">{t("shortcuts.command")}</th>
            <th scope="col">{t("shortcuts.keys")}</th>
            <th scope="col">{t("shortcuts.where")}</th>
          </tr>
        </thead>
        <tbody>
          {ids.map((id) => {
            const spec = commandOf(id);
            return (
              <tr
                key={id}
                data-command={id}
                className="border-t border-border align-top"
              >
                <th scope="row" className="py-1 pe-2 text-start font-normal">
                  {commandName(t, id)}
                </th>
                <td className="py-1 pe-2">
                  <KeyCaps bindings={spec.keys} />
                </td>
                <td className="py-1 text-sm text-fg-muted">
                  {whereOf(t, spec)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </section>
  );
}
