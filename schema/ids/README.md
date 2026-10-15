# Identifier vectors

`vectors.json` holds the test vectors for public identifiers (PRD §5.2, ADR-021). The Go package `server/internal/ids` and the TypeScript module `web/shared/src/ids.ts` both run every vector, so the two implementations cannot drift apart.

## The encoding

A public identifier is a type prefix, an underscore, and the entity's UUID encoded as 26 characters of Crockford base32: `nd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD`.

- **Body.** The 128 bits of the UUID, most significant first, with two zero bits in front to make 130 bits, split into 26 groups of 5 bits. Each group is one character of `0123456789ABCDEFGHJKMNPQRSTVWXYZ`. The first character is therefore always `0` to `7`. The alphabet is in ASCII order, so sorting the bodies as strings sorts the UUIDs, and UUIDv7 bodies sort by creation time.
- **Prefix.** One per entity type, listed under `prefixes` and in PRD §5.2.
- **Encoding** always writes the prefix in lowercase and the body in uppercase.

Decoding is case-insensitive, in the prefix and in the body (ADR-021). Case folding is ASCII only, so a non-ASCII character that Unicode folds to an ASCII letter, such as the Kelvin sign, is not accepted. The letters `I`, `L`, `O`, and `U` are not in the alphabet and are rejected. Crockford's optional reading of `I` and `L` as `1` and `O` as `0` is not applied, so every UUID has exactly one canonical spelling. Decoding accepts any 128-bit value, not only UUIDv7, because the encoding is bijective with the UUID.

## Decode errors

When an input has several problems, the first applicable check in this order decides the error. Every error after `unknown_prefix` also names the entity type of the prefix.

| Error | Meaning |
| --- | --- |
| `missing_prefix` | The input contains no underscore. |
| `unknown_prefix` | The text before the first underscore is not a prefix in the table. |
| `wrong_prefix` | The prefix belongs to another entity type than the one expected. |
| `character` | The body contains a character outside the alphabet, including any non-ASCII character. |
| `length` | The body is not 26 characters long. |
| `overflow` | The first character of the body is above `7`, so the value does not fit in 128 bits. |

The character check runs before the length check so that both implementations agree: Go counts UTF-8 bytes and TypeScript counts UTF-16 units, but both reject any non-ASCII character before counting.

## UUIDv7 generation

`uuidv7` pins the layout of RFC 9562 §5.7: a 48-bit big-endian Unix timestamp in milliseconds, the version `7`, 12 random bits, the variant `10`, and 62 random bits, which leaves the 74 random bits that PRD §5.2 relies on. Each vector gives the timestamp and 10 random bytes. The version and variant overwrite the top 4 bits of the first random byte and the top 2 bits of the third, whatever they were.

## The file

| Key | Contents |
| --- | --- |
| `prefixes` | Every entity type and its prefix. Each implementation's table must equal it exactly. |
| `encode` | A type, a UUID, and its canonical identifier. Encoding the UUID gives the identifier, and decoding the identifier gives the UUID. |
| `decode` | Non-canonical spellings that decode to the UUID, and their canonical form. |
| `decode_errors` | Inputs that fail to decode as `kind`, with the expected error and, when the prefix is known, its type as `got`. Decoding them without an expected type fails the same way, except `wrong_prefix` inputs, which then give the same result as decoding them as `got`. |
| `uuidv7` | A timestamp, 10 random bytes in hex, and the UUIDv7 built from them. |

UUIDs are written in the lowercase hyphenated form of RFC 9562. A `note` says what a vector is for.

The vectors were computed with arbitrary-precision integer arithmetic, independently of both implementations, so that a shared mistake in the bit handling cannot pass. Add vectors by hand and check them the same way. The PRD's own example decodes to a valid UUIDv7, and the RFC 9562 appendix A.6 example appears both as a UUIDv7 and as an identifier.
