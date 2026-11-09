// The image of an entity of a project's content (PRD FR-ASSET-02), as a
// component's image and an element's cover: its thumbnail, and, for an
// editor, its upload and its removal, each a change of its own, expecting
// the revision that the page showed as the user asked for it.

import { useMutation, useQuery } from "@tanstack/react-query";
import { type ChangeEvent, useId } from "react";
import { useTranslation } from "react-i18next";
import { api, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { useProjectDocument } from "../document/react";
import type { Command } from "../project/queries";
import { Button } from "../ui/Button";
import { Alert } from "../ui/Page";
import { imageTypes, thumbnailOf } from "./assets";

/** What the image of an entity shows, and says of it. */
export interface AssetImageProps {
  projectId: string;
  /** The entity's image, or undefined for none. */
  assetId: string | undefined;
  /** The entity's revision, as the page shows it, which each change expects. */
  rev: number;
  /** Whether the user may change the image. */
  editable: boolean;
  /**
   * Returns the command that sets the entity's image to the asset of
   * assetId, or, of undefined, takes it away, expecting rev.
   */
  command: (assetId: string | undefined, rev: number) => Command;
  /** What the section says: its heading, of no image, the image's text, and its controls'. */
  labels: {
    heading: string;
    none: string;
    alt: string;
    upload: string;
    replace: string;
    remove: string;
  };
}

/** The image of an entity, with its upload and its removal. */
export function AssetImage({
  projectId,
  assetId,
  rev,
  editable,
  command,
  labels,
}: AssetImageProps) {
  const { t } = useTranslation();
  const inputId = useId();
  const session = useQuery(sessionQuery).data;
  const doc = useProjectDocument(projectId);
  // An upload and the command that sets the image to what it uploaded are
  // one request of the page's, which goes on to set the image once the
  // section has gone, as once the user opened another entity meanwhile,
  // rather than leave the asset uploaded and the entity without it. The
  // removal, without a file, is the command alone. Each attempt says what
  // became of it alone, rather than what became of the one before it.
  const image = useMutation({
    mutationFn: async ({ file, rev }: { file?: File; rev: number }) => {
      const asset =
        file === undefined
          ? undefined
          : (
              await api.call("uploadAsset", {
                path: { project_id: projectId },
                headers: csrfHeaders(session),
                body: file,
              })
            ).body;
      return doc.send([command(asset?.id, rev)]);
    },
  });
  const chosen = (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.currentTarget;
    const file = input.files?.[0];
    if (file === undefined) {
      return;
    }
    image.mutate(
      { file, rev },
      {
        // The same file chosen again is a change of the input's own.
        onSettled: () => {
          input.value = "";
        },
      },
    );
  };
  const pending = image.isPending;
  const refusal = refusalOf(image.error, t);
  return (
    <section className="space-y-2">
      <h3 className="text-lg font-semibold">{labels.heading}</h3>
      {assetId === undefined ? (
        <p className="text-fg-muted">{labels.none}</p>
      ) : (
        <img
          src={thumbnailOf(projectId, assetId)}
          alt={labels.alt}
          className="max-h-32 max-w-full rounded-md border border-border"
        />
      )}
      <Alert message={refusal?.message} />
      {editable && (
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex flex-col gap-1">
            <label htmlFor={inputId} className="font-medium">
              {assetId === undefined ? labels.upload : labels.replace}
            </label>
            <input
              id={inputId}
              type="file"
              accept={imageTypes}
              disabled={pending}
              onChange={chosen}
              className="min-h-11 max-w-full file:me-2 file:min-h-11 file:cursor-pointer file:rounded-md file:border file:border-border file:bg-surface file:px-4 file:text-fg"
            />
          </div>
          {assetId !== undefined && (
            <Button disabled={pending} onClick={() => image.mutate({ rev })}>
              {labels.remove}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
