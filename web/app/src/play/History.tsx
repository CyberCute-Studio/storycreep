// Play mode's history (PRD FR-PLAY-04, plan note N79): each step of the
// flow, the start's first, with the choice that it took, the nodes that it
// passed and came to, why the flow ended, its runtime errors, and the
// values set by hand after it, and a button that copies it all as text,
// with the values that the flow holds now, for a bug report (AC2).

import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { copyText } from "../errors/copy";
import { Button } from "../ui/Button";
import { Status } from "../ui/Page";
import type { PlayState } from "./session";
import { historyLines, historyText, type Where } from "./steps";
import type { GroupNames } from "./values";

export function History({
  state,
  project,
  where,
  names,
}: {
  state: Extract<PlayState, { status: "playing" }>;
  /** The project's name, which the text's title says. */
  project: string;
  where: Where;
  names: GroupNames;
}) {
  const { t } = useTranslation();
  const titleId = useId();
  const area = useRef<HTMLElement>(null);
  const { flow, projection, turn } = state;
  const history = flow.history;
  // Whether the last copy copied, and of which history, at which turn,
  // which the panel says until a value set, or a turn, as a step, a step
  // back, or a reset, makes another history: a step back gives back the
  // very history of before the step, copied or not, which its turn tells
  // apart, so that the panel does not say anew that it copied it.
  const [copy, setCopy] = useState<{
    readonly of: typeof history;
    readonly turn: number;
    readonly copied: boolean;
  }>();
  const copied =
    copy?.of === history && copy.turn === turn ? copy.copied : undefined;
  const copyHistory = async () => {
    const text = historyText(t, project, flow, projection, where, names);
    const of = history;
    setCopy({
      of,
      turn,
      copied: await copyText(text, area.current ?? document.body),
    });
  };
  return (
    <section ref={area} aria-labelledby={titleId} className="space-y-2">
      <h3 id={titleId} className="font-semibold">
        {t("play.history.title")}
      </h3>
      <ol className="list-decimal space-y-2 ps-6">
        {historyLines(t, flow, projection, where, names).map(
          ({ heading, lines }, i) => (
            // The steps are a list that a step adds to, at its end, and
            // that Step back takes the last of.
            // biome-ignore lint/suspicious/noArrayIndexKey: a step is its place in the list
            <li key={i}>
              <p className="font-medium break-words">{heading}</p>
              {lines.length > 0 && (
                <ul className="space-y-0.5 text-sm">
                  {lines.map((line, j) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: the lines of a step, in its order
                    <li key={j} className="break-words">
                      {line}
                    </li>
                  ))}
                </ul>
              )}
            </li>
          ),
        )}
      </ol>
      <Status
        message={
          copied === undefined
            ? undefined
            : copied
              ? t("play.history.copied")
              : t("play.history.copy_failed")
        }
      />
      <Button onClick={() => void copyHistory()}>
        {t("play.history.copy")}
      </Button>
    </section>
  );
}
