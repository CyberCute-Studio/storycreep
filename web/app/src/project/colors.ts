// The palette of a project's boards and nodes (PRD FR-PROJ-02, FR-EDIT-06):
// none, or a name of it, which the board's settings and the canvas's
// inspector name in the page's locale.

import type { Board } from "@storycreep/shared/api";
import type { useTranslation } from "react-i18next";

/** A colour of the palette, or none. */
export type PaletteColor = Board["color"];

/** The colours that a board or a node may have: none, or a name of the palette. */
export const paletteColors = [
  "none",
  "red",
  "orange",
  "yellow",
  "green",
  "teal",
  "blue",
  "purple",
  "pink",
  "gray",
] as const satisfies readonly PaletteColor[];

/** Returns the name of a colour of the palette, in the page's locale. */
export function colorName(
  t: ReturnType<typeof useTranslation>["t"],
  color: PaletteColor,
): string {
  switch (color) {
    case "none":
      return t("board.color_none");
    case "red":
      return t("board.color_red");
    case "orange":
      return t("board.color_orange");
    case "yellow":
      return t("board.color_yellow");
    case "green":
      return t("board.color_green");
    case "teal":
      return t("board.color_teal");
    case "blue":
      return t("board.color_blue");
    case "purple":
      return t("board.color_purple");
    case "pink":
      return t("board.color_pink");
    case "gray":
      return t("board.color_gray");
  }
}
