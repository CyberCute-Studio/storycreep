// A reader of JSON that keeps each number as it is written, so that a value
// is read by its declaration rather than by the double that JSON.parse makes
// of it (PRD §5.8.9), and that refuses what no file of the published format
// holds, as the Go package server/internal/value's CheckJSON does: null
// anywhere (§6.2), an escape of a lone surrogate, which is no character, and
// a number written with an exponent beyond ±999, as 0e1000.

/** The greatest exponent, either way, that a number may be written with. */
export const MAX_EXPONENT = 999;

/**
 * How deeply arrays and objects may nest, as deeply as Go's encoding/json
 * reads them.
 */
export const MAX_JSON_DEPTH = 10000;

/** A JSON number, as it is written. */
export class JsonNumber {
  readonly raw: string;

  constructor(raw: string) {
    this.raw = raw;
  }

  /** The double nearest the number, as JSON.parse reads it. */
  get value(): number {
    return Number(this.raw);
  }
}

/** A JSON value as readJSON reads it: an object's prototype is null. */
export type JsonValue =
  | boolean
  | string
  | JsonNumber
  | JsonValue[]
  | { [key: string]: JsonValue };

/** The error of a text that readJSON refuses. */
export class JsonError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "JsonError";
  }
}

/**
 * Returns the JSON value that text holds, as the Go package reads one: an
 * object of the key that it has twice the last, as a consumer reads it, with
 * a null prototype, so that a key such as __proto__ is a key like any other;
 * an array; a number as the JsonNumber that it is written as; a string; and
 * a boolean. It throws a JsonError of a text that is not one JSON value, or
 * that holds null anywhere, a lone surrogate, escaped or not, a number
 * written with an exponent beyond ±MAX_EXPONENT, or arrays and objects
 * nested more than MAX_JSON_DEPTH deep. It never recurses.
 */
export function readJSON(text: string): JsonValue {
  return new Reader(text).document();
}

/**
 * Returns bytes if it is a view of a fixed-length ArrayBuffer, the one
 * buffer of which both WebCrypto and a browser's TextDecoder read a view,
 * and otherwise a copy of its bytes, made at once, over one (copyOf): of a
 * view of a resizable ArrayBuffer or of a SharedArrayBuffer, which they read
 * none of (WebIDL's BufferSource and AllowSharedBufferSource), or of another
 * realm's ArrayBuffer, which instanceof does not tell from those. What it
 * returns is a copy exactly when it is not bytes.
 */
export function fixedView(bytes: Uint8Array): Uint8Array<ArrayBuffer> {
  const buffer = bytes.buffer;
  return buffer instanceof ArrayBuffer && !buffer.resizable
    ? (bytes as Uint8Array<ArrayBuffer>)
    : copyOf(bytes);
}

/**
 * Returns a copy of bytes, a Uint8Array over a fixed-length ArrayBuffer of
 * its own, whatever the class of bytes: slice would return a view of the
 * same memory of a Node Buffer, or of a bundler's Buffer, whose slice is
 * subarray.
 */
export function copyOf(bytes: Uint8Array): Uint8Array<ArrayBuffer> {
  const copy = new Uint8Array(bytes.length);
  copy.set(bytes);
  return copy;
}

/**
 * Returns the JSON value of bytes, read as UTF-8, as readJSON reads a text,
 * or throws a JsonError of bytes that are not UTF-8, or that start with a
 * byte order mark, which no JSON text does. It reads bytes over any buffer,
 * though a browser's TextDecoder reads a view of a fixed-length ArrayBuffer
 * alone, since it decodes what fixedView returns of them.
 */
export function readJSONBytes(bytes: Uint8Array): JsonValue {
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(
      fixedView(bytes),
    );
  } catch {
    throw new JsonError("is not UTF-8");
  }
  return readJSON(text);
}

/**
 * Returns v as JSON.parse would have read it, each number the double nearest
 * it, and each object a plain one, without recursing; but for the key omit
 * of each object, if it is given, which it leaves out, with its value
 * unread, as the loader leaves out a board's ASTs, which it decodes from v
 * itself.
 */
export function plain(v: JsonValue, omit?: string): unknown {
  const root: { value?: unknown } = {};
  const stack: [JsonValue, (x: unknown) => void][] = [
    [
      v,
      (x) => {
        root.value = x;
      },
    ],
  ];
  for (let top = stack.pop(); top !== undefined; top = stack.pop()) {
    const [from, put] = top;
    if (from instanceof JsonNumber) {
      put(from.value);
    } else if (Array.isArray(from)) {
      const out: unknown[] = new Array(from.length);
      put(out);
      from.forEach((item, i) => {
        stack.push([
          item,
          (x) => {
            out[i] = x;
          },
        ]);
      });
    } else if (typeof from === "object") {
      const out: Record<string, unknown> = {};
      put(out);
      // Each key is set in its order, and set again once its value is read.
      for (const key of Object.keys(from)) {
        if (key === omit) {
          continue;
        }
        own(out, key, undefined);
        stack.push([
          from[key],
          (x) => {
            own(out, key, x);
          },
        ]);
      }
    } else {
      put(from);
    }
  }
  return root.value;
}

// own sets the key of o to x, as an assignment does, but for __proto__, which
// an assignment would take as o's prototype, and which it defines as an own
// key, as JSON.parse does.
function own(o: Record<string, unknown>, key: string, x: unknown): void {
  if (key === "__proto__") {
    Object.defineProperty(o, key, {
      value: x,
      enumerable: true,
      writable: true,
      configurable: true,
    });
  } else {
    o[key] = x;
  }
}

/** Reports whether v is an object of readJSON's, neither an array nor a scalar. */
export function isJsonObject(v: unknown): v is { [key: string]: JsonValue } {
  return (
    typeof v === "object" &&
    v !== null &&
    !Array.isArray(v) &&
    !(v instanceof JsonNumber)
  );
}

/**
 * Reports whether raw, a JSON number, is written with an exponent beyond
 * ±MAX_EXPONENT, whatever its value, which it reads without its leading
 * zeros, however many.
 */
export function exponentBeyond(raw: string): boolean {
  const e = raw.search(/[eE]/);
  if (e < 0) {
    return false;
  }
  const magnitude = raw
    .slice(e + 1)
    .replace(/^[+-]/, "")
    .replace(/^0+/, "");
  return magnitude.length > 3 || Number(magnitude) > MAX_EXPONENT;
}

/**
 * The decimal of a JSON number as it is written: its sign, its significant
 * digits, without the zeros around them, and the power of ten of the first,
 * or none of a zero, whose digits are "".
 */
export interface Decimal {
  readonly negative: boolean;
  readonly digits: string;
  readonly exponent: number;
}

/**
 * Returns the decimal of raw, a JSON number, or a string of the grammar that
 * to_float reads, which may start with +, exactly, however many digits it
 * has, in time linear in its length. Of an exponent of more digits than a
 * double holds exactly, which no JSON number of a file is written with
 * (MAX_EXPONENT), its exponent is a double near its value, or an infinity,
 * which is beyond a double's range all the same.
 */
export function decimalOf(raw: string): Decimal {
  const negative = raw.startsWith("-");
  const s = negative || raw.startsWith("+") ? raw.slice(1) : raw;
  const e = s.search(/[eE]/);
  const mantissa = e < 0 ? s : s.slice(0, e);
  const shift = e < 0 ? 0 : Number(s.slice(e + 1));
  const point = mantissa.indexOf(".");
  const whole = point < 0 ? mantissa : mantissa.slice(0, point);
  const all = whole + (point < 0 ? "" : mantissa.slice(point + 1));
  // The zeros around the digits are counted rather than matched, as /0+$/
  // would match them, in time quadratic in the length of a run of zeros
  // that is not at the end.
  let lead = 0;
  while (lead < all.length && all[lead] === "0") {
    lead++;
  }
  let end = all.length;
  while (end > lead && all[end - 1] === "0") {
    end--;
  }
  const digits = all.slice(lead, end);
  return {
    negative: negative && digits !== "",
    digits,
    exponent: whole.length - lead - 1 + shift,
  };
}

/**
 * Returns -1, 0, or +1 as the JSON number a is less than, equal to, or
 * greater than b, exactly, as they are written.
 */
export function compareNumbers(a: string, b: string): number {
  const x = decimalOf(a);
  const y = decimalOf(b);
  if (x.digits === "" || y.digits === "") {
    const sx = x.digits === "" ? 0 : x.negative ? -1 : 1;
    const sy = y.digits === "" ? 0 : y.negative ? -1 : 1;
    return Math.sign(sx - sy);
  }
  if (x.negative !== y.negative) {
    return x.negative ? -1 : 1;
  }
  let c: number;
  if (x.exponent !== y.exponent) {
    c = x.exponent < y.exponent ? -1 : 1;
  } else {
    const n = Math.max(x.digits.length, y.digits.length);
    const dx = x.digits.padEnd(n, "0");
    const dy = y.digits.padEnd(n, "0");
    c = dx === dy ? 0 : dx < dy ? -1 : 1;
  }
  return x.negative ? -c : c;
}

/**
 * Reports whether the JSON number raw is an integer, by its decimal,
 * exactly, as JSON Schema's integer is a number whose fraction is zero,
 * however it is written, as 2.0.
 */
export function isIntegral(raw: string): boolean {
  const d = decimalOf(raw);
  return d.digits === "" || d.digits.length - 1 <= d.exponent;
}

// A container that the reader has opened: an array, or an object and the key
// of the value that it reads.
type Container =
  | { array: JsonValue[] }
  | { object: { [key: string]: JsonValue }; key: string };

const numberPattern = /-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/y;

// The characters that an escape but \u stands for, by the character after
// its backslash.
const simpleEscapes: Record<string, string> = {
  '"': '"',
  "\\": "\\",
  "/": "/",
  b: "\b",
  f: "\f",
  n: "\n",
  r: "\r",
  t: "\t",
};

class Reader {
  private i = 0;

  constructor(private readonly text: string) {}

  private fail(problem: string): JsonError {
    return new JsonError(problem);
  }

  // space steps past the white space at the next character: a space, a tab,
  // a line feed, or a carriage return.
  private space(): void {
    for (; this.i < this.text.length; this.i++) {
      const c = this.text.charCodeAt(this.i);
      if (c !== 0x20 && c !== 0x09 && c !== 0x0a && c !== 0x0d) {
        return;
      }
    }
  }

  document(): JsonValue {
    const v = this.value();
    this.space();
    if (this.i !== this.text.length) {
      throw this.fail("is not one JSON value");
    }
    return v;
  }

  // value reads a value, its arrays and objects by a stack of the containers
  // that are open, rather than by recursing.
  private value(): JsonValue {
    const stack: Container[] = [];
    for (;;) {
      this.space();
      let v: JsonValue;
      const c = this.text[this.i];
      if (c === "{" || c === "[") {
        // An array or an object that it opens is nested one deeper than
        // those that are open, empty or not, as encoding/json counts it.
        if (stack.length >= MAX_JSON_DEPTH) {
          throw this.fail(
            `nests arrays and objects more than ${MAX_JSON_DEPTH} deep`,
          );
        }
        this.i++;
        this.space();
        const object = c === "{";
        if (this.text[this.i] === (object ? "}" : "]")) {
          this.i++;
          v = object ? Object.create(null) : [];
        } else {
          stack.push(
            object
              ? { object: Object.create(null), key: this.key() }
              : { array: [] },
          );
          continue;
        }
      } else {
        v = this.scalar();
      }
      // v goes into the container that is open, and closes each that ends
      // after it, until one goes on with another value.
      for (;;) {
        const top = stack[stack.length - 1];
        if (top === undefined) {
          return v;
        }
        if ("array" in top) {
          top.array.push(v);
        } else {
          top.object[top.key] = v;
        }
        this.space();
        const next = this.text[this.i];
        if (next === ",") {
          this.i++;
          if (!("array" in top)) {
            this.space();
            top.key = this.key();
          }
          break;
        }
        if (next !== ("array" in top ? "]" : "}")) {
          throw this.fail("is not one JSON value");
        }
        this.i++;
        stack.pop();
        v = "array" in top ? top.array : top.object;
      }
    }
  }

  // key reads an object's key and the colon after it.
  private key(): string {
    if (this.text[this.i] !== '"') {
      throw this.fail("is not one JSON value");
    }
    const key = this.string();
    this.space();
    if (this.text[this.i] !== ":") {
      throw this.fail("is not one JSON value");
    }
    this.i++;
    return key;
  }

  // scalar reads a string, a number, true, or false.
  private scalar(): JsonValue {
    const c = this.text[this.i];
    if (c === '"') {
      return this.string();
    }
    if (c === "-" || (c >= "0" && c <= "9")) {
      return this.number();
    }
    for (const [word, v] of [
      ["true", true],
      ["false", false],
    ] as const) {
      if (this.text.startsWith(word, this.i)) {
        this.i += word.length;
        return v;
      }
    }
    if (this.text.startsWith("null", this.i)) {
      throw this.fail("holds null");
    }
    throw this.fail("is not one JSON value");
  }

  // number reads a number, which it keeps as it is written, once it has
  // checked that its exponent, if it has one, is within ±MAX_EXPONENT, read
  // without its leading zeros, however many.
  private number(): JsonNumber {
    numberPattern.lastIndex = this.i;
    const m = numberPattern.exec(this.text);
    if (m === null) {
      throw this.fail("is not one JSON value");
    }
    const raw = m[0];
    this.i += raw.length;
    if (exponentBeyond(raw)) {
      throw this.fail(
        `writes the number ${raw.slice(0, 40)} with an exponent beyond ±${MAX_EXPONENT}, as no number of a file is written`,
      );
    }
    return new JsonNumber(raw);
  }

  // string reads a string, from its opening quote to its closing one.
  private string(): string {
    this.i++;
    let out = "";
    let run = this.i;
    for (;;) {
      if (this.i >= this.text.length) {
        throw this.fail("is not one JSON value");
      }
      const c = this.text.charCodeAt(this.i);
      if (c === 0x22) {
        out += this.text.slice(run, this.i);
        this.i++;
        return out;
      }
      if (c < 0x20) {
        throw this.fail("is not one JSON value");
      }
      if (c >= 0xd800 && c <= 0xdfff) {
        // A pair, of a high surrogate and then a low one, is a character,
        // and anything else no text.
        const low = this.text.charCodeAt(this.i + 1);
        if (c > 0xdbff || !(low >= 0xdc00 && low <= 0xdfff)) {
          throw this.fail("holds a lone surrogate, which is not UTF-8");
        }
        this.i += 2;
        continue;
      }
      if (c !== 0x5c) {
        this.i++;
        continue;
      }
      out += this.text.slice(run, this.i);
      out += this.escape();
      run = this.i;
    }
  }

  // escape reads the escape at the next character, a backslash, and returns
  // the character that it stands for: a \u of a high surrogate is one with
  // the \u of a low one that follows it at once, and any other \u of a
  // surrogate no character.
  private escape(): string {
    const c = this.text[this.i + 1];
    if (c !== undefined && Object.hasOwn(simpleEscapes, c)) {
      this.i += 2;
      return simpleEscapes[c];
    }
    if (c !== "u") {
      throw this.fail("is not one JSON value");
    }
    const u = this.hex4(this.i + 2);
    this.i += 6;
    if (u >= 0xdc00 && u <= 0xdfff) {
      throw this.fail(
        "escapes a lone surrogate in a string, which is not a character",
      );
    }
    if (u >= 0xd800 && u <= 0xdbff) {
      const low = this.text.startsWith("\\u", this.i)
        ? this.hex4(this.i + 2)
        : -1;
      if (!(low >= 0xdc00 && low <= 0xdfff)) {
        throw this.fail(
          "escapes a lone surrogate in a string, which is not a character",
        );
      }
      this.i += 6;
      return String.fromCharCode(u, low);
    }
    return String.fromCharCode(u);
  }

  // hex4 returns the value of the four hexadecimal digits at i.
  private hex4(i: number): number {
    const digits = this.text.slice(i, i + 4);
    if (!/^[0-9a-fA-F]{4}$/.test(digits)) {
      throw this.fail("is not one JSON value");
    }
    return Number.parseInt(digits, 16);
  }
}
