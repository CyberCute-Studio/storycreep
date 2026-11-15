// Play mode's author view of hidden choices (PRD FR-PLAY-03, plan note
// N79): a box that shows, under the current node's choices, those whose
// condition was false as the flow built them, which a player does not see,
// each with its condition, which is false, and the parts of it that failed,
// with the values that they read. Unticked, the panel lists the choices
// that the runtime offers alone (AC2).

import { useId } from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "zustand";
import { CheckboxField } from "../ui/Field";
import type { HiddenChoice, PlaySession } from "./session";

export function HiddenChoices({
  session,
  hidden,
}: {
  session: PlaySession;
  /** The current node's hidden choices, in their author's order. */
  hidden: readonly HiddenChoice[];
}) {
  const { t } = useTranslation();
  const show = useStore(session.view, (v) => v.showHidden);
  const titleId = useId();
  return (
    <div className="space-y-2">
      <CheckboxField
        label={t("play.hidden.toggle")}
        hint={t("play.hidden.toggle_hint")}
        checked={show}
        onChange={(event) =>
          session.view.setState({ showHidden: event.currentTarget.checked })
        }
      />
      {show &&
        (hidden.length === 0 ? (
          <p className="text-sm text-fg-muted">{t("play.hidden.none")}</p>
        ) : (
          <section aria-labelledby={titleId} className="space-y-2">
            <h3 id={titleId} className="font-semibold">
              {t("play.hidden.title", { count: hidden.length })}
            </h3>
            <ul className="space-y-2">
              {hidden.map((h) => (
                <HiddenItem key={h.id} hidden={h} />
              ))}
            </ul>
          </section>
        ))}
    </div>
  );
}

function HiddenItem({ hidden }: { hidden: HiddenChoice }) {
  const { t } = useTranslation();
  const failed = hidden.failed;
  return (
    <li className="space-y-1 rounded-md border border-dashed border-border p-2">
      <p className="font-medium break-words whitespace-pre-line">
        {hidden.text === "" ? t("play.continue") : hidden.text}
      </p>
      <p className="text-sm">
        {failed === undefined
          ? t("play.hidden.not_compiled")
          : t("play.hidden.condition")}
      </p>
      <code className="block break-words whitespace-pre-wrap text-sm">
        {hidden.source}
      </code>
      {failed !== undefined && failed.length > 0 && (
        <>
          <p className="text-sm">
            {t("play.hidden.failed", { count: failed.length })}
          </p>
          <ul className="list-disc space-y-1 ps-5 text-sm">
            {failed.map((part, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: the parts are of one condition, in its order, which does not change
              <li key={i}>
                <code className="break-words whitespace-pre-wrap">
                  {part.source}
                </code>
                {part.reads.length > 0 && (
                  <ul className="text-fg-muted">
                    {part.reads.map((read) => (
                      <li key={read.name} className="break-words">
                        {t("play.hidden.read", {
                          name: read.name,
                          value: read.value,
                        })}
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
    </li>
  );
}
