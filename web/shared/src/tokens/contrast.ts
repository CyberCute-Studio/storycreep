// The contrast of two colours as WCAG 2.2 measures it (PRD NFR-A11Y-06):
// the tokens' test checks tokens.css's colours by it, and the canvas the
// colours that it marks nodes by, against its card and its board, and the
// lightness of the board that picks them, so that both measure alike.

/** Returns the relative luminance of a colour, 0xRRGGBB, from 0 to 1, as WCAG 2.2 defines it. */
export function relativeLuminance(color: number): number {
  const linear = (channel: number) => {
    const c = channel / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return (
    0.2126 * linear((color >> 16) & 0xff) +
    0.7152 * linear((color >> 8) & 0xff) +
    0.0722 * linear(color & 0xff)
  );
}

/** Returns the contrast ratio of colours a and b, each 0xRRGGBB, from 1 to 21, as WCAG 2.2 defines it. */
export function contrast(a: number, b: number): number {
  const [lighter, darker] = [relativeLuminance(a), relativeLuminance(b)].sort(
    (x, y) => y - x,
  );
  return (lighter + 0.05) / (darker + 0.05);
}
