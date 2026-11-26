// A node's position and size as the server keeps their numbers, in a module
// of its own, which imports nothing, so that what reads them, as play
// mode's projection, loads without the document's appliers, as the
// end-to-end cross-check of the projection loads it in Node (plan item 89).

/**
 * Returns x as the server keeps a position's or a size's number: in
 * hundredths, rounded half away from zero, read as the decimal that it is
 * written as (node/geometry.go), here as JavaScript writes it, which is how
 * a payload writes it, unless with more digits than a double holds.
 */
export function hundredths(x: number): number {
  const [mantissa, exponent = "0"] = String(Math.abs(x)).split("e");
  const [whole, fraction = ""] = mantissa.split(".");
  const digits = (whole + fraction).replace(/^0+/, "");
  if (digits === "") {
    return 0;
  }
  const shift = Number(exponent) - fraction.length + 2;
  let n: number;
  if (shift >= 0) {
    n = Number(digits + "0".repeat(shift));
  } else {
    // The first digit cut off rounds the rest up when it is 5 or more,
    // since what follows it can only add to it.
    const keep = digits.length + shift;
    n = keep > 0 ? Number(digits.slice(0, keep)) : 0;
    if (keep >= 0 && digits[keep] >= "5") {
      n++;
    }
  }
  return n === 0 ? 0 : (x < 0 ? -n : n) / 100;
}
