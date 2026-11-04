// A value that the user copies, as a new API token's secret or an
// invitation's link, which the server shows once: a field that selects it
// whole as it gets the focus, and a button that copies it, which says
// whether it could.

import { type ReactNode, useRef, useState } from "react";
import { copyText } from "../errors/copy";
import { Button } from "./Button";
import { TextField } from "./Field";
import { Status } from "./Page";

export interface CopyFieldProps {
  label: string;
  value: string;
  /** The button's label, and what the field says once it copied, or could not. */
  messages: { copy: string; copied: string; failed: string };
  /** What goes beside the button that copies, as a dialog's Done. */
  children?: ReactNode;
}

/** A field of value, which the user copies with its button. */
export function CopyField({
  label,
  value,
  messages,
  children,
}: CopyFieldProps) {
  const [copied, setCopied] = useState<boolean>();
  const area = useRef<HTMLDivElement>(null);
  const copy = async () => {
    // Within the dialog, if the field is in one, whose focus the copy keeps.
    setCopied(await copyText(value, area.current ?? document.body));
  };
  return (
    <div ref={area} className="space-y-4">
      <TextField
        label={label}
        value={value}
        readOnly
        spellCheck={false}
        className="min-h-11 w-full rounded-md border border-border bg-surface px-3 font-mono text-fg"
        onFocus={(event) => event.currentTarget.select()}
      />
      <Status
        message={
          copied === undefined
            ? undefined
            : copied
              ? messages.copied
              : messages.failed
        }
      />
      <div className="flex flex-wrap justify-end gap-2">
        <Button onClick={() => void copy()}>{messages.copy}</Button>
        {children}
      </div>
    </div>
  );
}
