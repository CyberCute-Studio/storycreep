// Entity identifiers and their public form (PRD §5.2, ADR-021).
//
// Every entity's primary key is a UUIDv7. Everywhere an identifier is
// exchanged, it is written as a type prefix, an underscore, and the UUID in 26
// characters of Crockford base32: "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD". Decoding
// checks the prefix against the expected entity type, so a board ID passed
// where a node ID is expected fails with an IdDecodeError.
//
// This is the TypeScript twin of the Go package server/internal/ids. Both run
// the vectors in schema/ids, whose README states the rules.

/** The prefix of each entity type's identifiers, without the underscore. */
export const ID_PREFIXES = {
  workspace: "ws",
  user: "usr",
  membership: "mem",
  invite: "inv",
  api_token: "tok",
  session: "ses",
  project: "prj",
  folder: "fld",
  board: "bd",
  node: "nd",
  branch_output: "bo",
  connection: "cn",
  variable: "var",
  component_type: "ct",
  attribute_def: "atr",
  component: "cmp",
  comment: "cmt",
  snapshot: "snp",
  operation: "op",
  asset: "ast",
  job: "job",
  play_state: "pst",
} as const;

/** An entity type that has a public identifier. */
export type IdKind = keyof typeof ID_PREFIXES;

const KINDS_BY_PREFIX: ReadonlyMap<string, IdKind> = new Map(
  (Object.keys(ID_PREFIXES) as IdKind[]).map((kind) => [
    ID_PREFIXES[kind],
    kind,
  ]),
);

function isIdKind(kind: string): kind is IdKind {
  return Object.hasOwn(ID_PREFIXES, kind);
}

// Crockford's base32 alphabet. It is in ASCII order, so encoded bodies sort in
// the same order as the UUIDs.
const ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";

// The length of an encoded UUID: 128 bits in 5-bit characters, padded with two
// zero bits in front.
const BODY_LENGTH = 26;

// Maps an ASCII code to its value in the alphabet, accepting lowercase
// letters, or to -1.
const DECODE_TABLE = (() => {
  const table = new Int8Array(128).fill(-1);
  for (let value = 0; value < ALPHABET.length; value++) {
    const code = ALPHABET.charCodeAt(value);
    table[code] = value;
    table[String.fromCharCode(code).toLowerCase().charCodeAt(0)] = value;
  }
  return table;
})();

/** Why an identifier could not be decoded, as named in schema/ids/vectors.json. */
export type IdDecodeReason =
  | "missing_prefix"
  | "unknown_prefix"
  | "wrong_prefix"
  | "character"
  | "length"
  | "overflow";

const REASON_TEXT: Record<IdDecodeReason, string> = {
  missing_prefix: "it has no type prefix",
  unknown_prefix: "its type prefix is unknown",
  wrong_prefix: "its prefix is for another type",
  character: "it contains a character outside Crockford base32",
  length: `the part after the prefix is not ${BODY_LENGTH} characters long`,
  overflow: "the part after the prefix does not fit in 128 bits",
};

/**
 * An identifier that could not be decoded. It does not hold the input, which
 * may come from a user and may be long.
 */
export class IdDecodeError extends Error {
  /** Why decoding failed. */
  readonly reason: IdDecodeReason;
  /** The entity type the identifier had to be, if one was expected. */
  readonly expected: IdKind | undefined;
  /** The entity type named by the prefix, unless it is missing or unknown. */
  readonly got: IdKind | undefined;

  constructor(
    reason: IdDecodeReason,
    expected: IdKind | undefined,
    got: IdKind | undefined,
  ) {
    const what =
      expected === undefined ? "identifier" : `identifier for type ${expected}`;
    const why =
      reason === "wrong_prefix" && got !== undefined
        ? `its prefix ${ID_PREFIXES[got]}_ is for type ${got}`
        : REASON_TEXT[reason];
    super(`invalid ${what}: ${why}`);
    this.name = "IdDecodeError";
    this.reason = reason;
    this.expected = expected;
    this.got = got;
  }
}

/**
 * Returns the public identifier of the entity of the given type with the
 * given 16-byte UUID: the lowercase prefix, an underscore, and the uppercase
 * body.
 */
export function encodeId(kind: IdKind, uuid: Uint8Array): string {
  if (!isIdKind(kind)) {
    throw new RangeError(`${String(kind)} is not an entity type`);
  }
  if (uuid.length !== 16) {
    throw new RangeError(`a UUID has 16 bytes, not ${uuid.length}`);
  }
  let body = "";
  // Emit 5 bits at a time, most significant first. The two zero bits that pad
  // the 128 bits to 130 start out in the accumulator.
  let acc = 0;
  let bits = 2;
  for (const byte of uuid) {
    acc = (acc << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      body += ALPHABET[(acc >>> bits) & 0x1f];
    }
    acc &= (1 << bits) - 1;
  }
  return `${ID_PREFIXES[kind]}_${body}`;
}

/**
 * Returns the UUID of the public identifier, which must be the identifier of
 * an entity of the given type. Prefix and body are case-insensitive.
 *
 * @throws {IdDecodeError} if the identifier is malformed or of another type.
 */
export function decodeId(kind: IdKind, id: string): Uint8Array {
  const decoded = decode(kind, id);
  if (decoded.kind !== kind) {
    throw new IdDecodeError("wrong_prefix", kind, decoded.kind);
  }
  return decoded.uuid;
}

/**
 * Returns the entity type and the UUID of the public identifier, whatever its
 * type.
 *
 * @throws {IdDecodeError} if the identifier is malformed.
 */
export function decodeAnyId(id: string): { kind: IdKind; uuid: Uint8Array } {
  return decode(undefined, id);
}

// Parses an identifier into its type and UUID. The checks run in the order of
// the table in schema/ids/README.md, which decides the error when several
// apply. A type mismatch with expected is left to the caller, so that it is
// reported before any problem with the body.
function decode(
  expected: IdKind | undefined,
  id: string,
): { kind: IdKind; uuid: Uint8Array } {
  const sep = id.indexOf("_");
  if (sep < 0) {
    throw new IdDecodeError("missing_prefix", expected, undefined);
  }
  // Fold ASCII letters only: toLowerCase would turn the Kelvin sign into "k".
  const prefix = id
    .slice(0, sep)
    .replace(/[A-Z]/g, (c) => String.fromCharCode(c.charCodeAt(0) + 32));
  const kind = KINDS_BY_PREFIX.get(prefix);
  if (kind === undefined) {
    throw new IdDecodeError("unknown_prefix", expected, undefined);
  }
  if (expected !== undefined && kind !== expected) {
    throw new IdDecodeError("wrong_prefix", expected, kind);
  }
  const body = id.slice(sep + 1);
  const values = new Int8Array(body.length);
  for (let i = 0; i < body.length; i++) {
    const code = body.charCodeAt(i);
    const value = code < 128 ? DECODE_TABLE[code] : -1;
    if (value < 0) {
      throw new IdDecodeError("character", expected, kind);
    }
    values[i] = value;
  }
  if (body.length !== BODY_LENGTH) {
    throw new IdDecodeError("length", expected, kind);
  }
  if (values[0] > 7) {
    throw new IdDecodeError("overflow", expected, kind);
  }
  const uuid = new Uint8Array(16);
  // Take in 5 bits at a time and emit whole bytes. Starting at -2 drops the
  // two padding bits of the first character, which the check above proved
  // zero.
  let acc = 0;
  let bits = -2;
  let j = 0;
  for (const value of values) {
    acc = (acc << 5) | value;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      uuid[j++] = acc >>> bits;
      acc &= (1 << bits) - 1;
    }
  }
  return { kind, uuid };
}

/**
 * Returns a new UUIDv7 (RFC 9562 §5.7) for the current time: a 48-bit Unix
 * timestamp in milliseconds followed by 74 random bits. UUIDs made in
 * different milliseconds sort by time; within one millisecond their order is
 * random.
 */
export function newUuid(): Uint8Array {
  return uuidV7From(Date.now(), crypto.getRandomValues(new Uint8Array(10)));
}

/** Returns the public identifier of a new entity of the given type. */
export function newId(kind: IdKind): string {
  return encodeId(kind, newUuid());
}

/**
 * Builds a UUIDv7 from a Unix time in milliseconds and 10 random bytes. The
 * version and variant bits replace the top 4 bits of random[0] and the top 2
 * bits of random[2].
 */
export function uuidV7From(unixMs: number, random: Uint8Array): Uint8Array {
  if (!Number.isSafeInteger(unixMs) || unixMs < 0 || unixMs >= 2 ** 48) {
    throw new RangeError(`${unixMs} is not a 48-bit Unix time in milliseconds`);
  }
  if (random.length !== 10) {
    throw new RangeError(
      `a UUIDv7 needs 10 random bytes, not ${random.length}`,
    );
  }
  const uuid = new Uint8Array(16);
  const high = Math.floor(unixMs / 2 ** 32);
  const low = unixMs >>> 0;
  uuid[0] = high >>> 8;
  uuid[1] = high;
  uuid[2] = low >>> 24;
  uuid[3] = low >>> 16;
  uuid[4] = low >>> 8;
  uuid[5] = low;
  uuid.set(random, 6);
  uuid[6] = 0x70 | (uuid[6] & 0x0f);
  uuid[8] = 0x80 | (uuid[8] & 0x3f);
  return uuid;
}

const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Returns the 16-byte UUID in the lowercase hyphenated form of RFC 9562, such
 * as "017f22e2-79b0-7cc3-98c4-dc0c0c07398f".
 */
export function formatUuid(uuid: Uint8Array): string {
  if (uuid.length !== 16) {
    throw new RangeError(`a UUID has 16 bytes, not ${uuid.length}`);
  }
  const hex = Array.from(uuid, (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

/**
 * Parses a UUID in the hyphenated form of RFC 9562. The hexadecimal digits are
 * case-insensitive. Client-generated values without a type prefix, such as
 * command IDs, use this form.
 *
 * @throws {SyntaxError} if the text is not a hyphenated UUID.
 */
export function parseUuid(text: string): Uint8Array {
  if (!UUID_PATTERN.test(text)) {
    throw new SyntaxError(
      "not a UUID in the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    );
  }
  const hex = text.replaceAll("-", "");
  const uuid = new Uint8Array(16);
  for (let i = 0; i < 16; i++) {
    uuid[i] = Number.parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  }
  return uuid;
}
