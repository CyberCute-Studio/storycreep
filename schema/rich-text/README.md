# Rich text

The rich text of a project's content, node bodies, connection labels, and component descriptions, is a ProseMirror document of a constrained schema, stored as JSON (PRD FR-TEXT-01, ADR-020, plan note N40). The schema's nodes and marks are TipTap's, so that the editor's extensions, with only the attributes that the table below gives them, write the documents that the server stores. ProseMirror writes every attribute of a node, `null` when it is not set, as a value reference's `decimals`; the editor leaves such an attribute out before it sends a document, since the server refuses a `null` wherever it is in a document.

| File | Contents |
| --- | --- |
| [`v1/rich-text.schema.json`](v1/rich-text.schema.json) | The JSON Schema of a document, version 1, with examples. Its `$id` is the URL of the file on the main branch. `server/internal/richtext` generates it, and `go test ./...` fails when it is out of date; `go test ./internal/richtext -run TestSchemaFile -update`, in `server/`, rewrites it. It is shaped so that a validator's work grows with a document's size, whatever the document holds: of the branches of each `oneOf`, only one holds blocks, so both kinds of list are one definition, `list`, and no array is `uniqueItems`. It has only the keywords that the API's OpenAPI tooling reads, since the API's document holds it for a field of rich text, so it has no `not`, and no `oneOf` beside an object's properties, which a bulleted list's lack of `attrs` would need. |
| [`vectors.json`](vectors.json) | The test vectors of the validation and of the derivations below. The Go package `server/internal/richtext` runs every vector, and so will the TypeScript client, the editor's validation (plan item 57) and the play mode's segments (item 71), so that the implementations cannot drift apart. |

## The document

| Node | Fields | Holds |
| --- | --- | --- |
| `doc` | `content` | At least one block: `paragraph`, `bulletList`, or `orderedList`. The editor's empty document is one empty paragraph. |
| `paragraph` | `content`, optional | Inline nodes: `text`, `hardBreak`, `mention`, and `valueRef`. |
| `bulletList` | `content` | At least one `listItem`. |
| `orderedList` | `attrs.start`, optional, from 0 to 999,999, 1 when it is not given; `content` | At least one `listItem`. |
| `listItem` | `content` | A `paragraph`, then any blocks. |
| `text` | `text`, at least one character; `marks`, optional | Text, without a control character (Unicode's `Cc`) or a line or paragraph separator (U+2028, U+2029): a line break is a `hardBreak`, and a paragraph a `paragraph`. |
| `hardBreak` | `marks`, optional | A line break within a paragraph. |
| `mention` | `attrs.id`; `marks`, optional | A component (`cmp_…`), which the text shows by its name (FR-TEXT-03). |
| `valueRef` | `attrs.ref`, `attrs.attribute`, `attrs.decimals`; `marks`, optional | A value, which the text shows as the runtime renders it (FR-TEXT-02): a variable's (`ref` is a `var_…`, and there is no `attribute`), or a component's runtime attribute's (`ref` is a `cmp_…` and `attribute` the attribute definition's `atr_…`). `decimals`, from 0 to 6, shows a number with that many digits after the point, rounded half away from zero (PRD §5.8.9); a value that is not a number is shown as it is. |

The marks are `bold`, `italic`, `strike`, `code`, and `link`, whose `attrs.href` is the URL. A node has each mark at most once, and the code mark with no other, as the editor's code mark excludes the others. Every node and mark is an object with a `type`, and has no field that the table does not give it, so `attrs` on a paragraph or on a bulleted list, or a link's `target`, is refused, and no field is `null`, which is refused with `wrong_type`. An ID is read regardless of case, as [`../ids/README.md`](../ids/README.md) says. A number is read as `JSON.parse` reads it, as the nearest double: `3.0` and `3e0` are 3, and so is `2.99999999999999999999`, which has more digits than a double holds; a number too large for a double, as `1e400`, which `JSON.parse` reads as `Infinity`, is `out_of_range`.

A link has at most 2,048 characters, counted as code points (not as JavaScript's `length` counts them, in UTF-16 code units), none of them white space (Unicode's `White_Space`), a control character (`Cc`), or a format character (`Cf`, as U+200B, U+202E, or U+FEFF), which would hide or reorder what a reader sees of it (PRD NFR-SEC-08), and each `%` in it starts an escape, `%` and two hexadecimal digits. Its scheme, which is read regardless of ASCII case and of no other, is one of these:

- `mailto:`, followed by an address, what is before the first `?`, which is not empty, and then, after that `?`, its query. Each of the query's fields, which `&` separates, is empty, or a name followed by `=` and its value, and the name, read regardless of ASCII case, is `to`, `cc`, `bcc`, `subject`, or `body`, so that no link sets a field, as `attach`, for which a mail client could read a file. A `mailto:` link has no `#`, which RFC 6068 gives it no use for, and after which a mail client that reads the fields after the first `?` and between the `&`s, without cutting a fragment off, would read a field, as `attach` in `mailto:keeper@example.com#?attach=/etc/passwd`; a `#` in a field's value is escaped, `%23`. Neither the address nor a field's value but `body`'s escapes a control character, `%00` to `%1F` or `%7F`, with its digits in either case, which a mail client could write into a header, as a line break that starts another header in `mailto:keeper@example.com%0D%0ABcc:evil@example.com`; a body's line breaks are `%0D%0A`, as RFC 6068 writes them.
- `http://` or `https://`, followed by an authority, what is before the first `/`, `?`, or `#`, which is a host, then `:` and a port when it has one. The authority has no `@`, so a link has no userinfo, which would show a host that the link does not lead to, as `https://example.com@evil.example/` leads to `evil.example`. The host is not empty, and is ASCII. When the authority starts with `[`, the host is what the brackets hold, an IPv6 address as RFC 4291 (section 2.2) writes it, without a zone: eight groups of one to four hexadecimal digits, in either case, separated by `:`, of which one run of one or more groups of zeros may be written as `::`, and the last two of which may be written as an IPv4 address, below; and the `]` is followed by nothing or by `:` and the port. Otherwise the host is a name, which the last `:` of the authority, when it has one, separates from the port: ASCII letters and digits and the characters ``-._~!$&'()*+,;=``, in labels that `.` separates, of which none is empty but a last one after a `.` that ends the name, as in `example.com.`. When the name's last label, without that empty one, is ASCII digits, or `0x` or `0X` and hexadecimal digits, which a browser then reads as an IPv4 address, the name is one: four numbers from 0 to 255 in decimal, without leading zeros, separated by `.`, as `192.0.2.1`, so `https://999/`, `https://127.1/`, and `https://example.123/` are refused. The port is ASCII digits whose value is at most 65,535, as `08080` is 8080, or nothing. So `https://:80/` has no host, and a backslash, which a browser reads as a slash, cannot hide another host in the authority, as in `https://example.com\@evil.example/`, nor can a character beyond ASCII, which may look like another: a letter as a slash, as U+30CE KATAKANA LETTER NO (`ノ`), a digit as a dot, as U+0660 ARABIC-INDIC DIGIT ZERO (`٠`), or a symbol as either, as U+2215 DIVISION SLASH, all of which IDNA keeps in a host, so `https://bank.exampleノlogin.evil.example/`, which reads as a path of `bank.example`, leads to a host of `evil.example`. An internationalized host is written as IDNA writes it in ASCII, as `xn--caf-dma.example` for `café.example`, which is what a browser's URL parser gives as a URL's host (`new URL(href).hostname`), so the editor writes a link's host so before it sends the document. Whether a label that starts with `xn--` is one that IDNA writes is not checked, as whether a host exists is not: such a link leads nowhere, rather than elsewhere.

What follows the authority, and a `mailto:` link's address and its fields' values, may hold any character but those above, as `|`, `{`, and `@`. These are the rules, rather than what a URL parser takes, since parsers differ on what is not a URL. `javascript:`, `data:`, `file:`, and relative URLs are refused.

### Limits and what the server checks beyond the schema

Every document that the server accepts validates against the JSON Schema. The server also refuses what JSON Schema does not express, and what the schema leaves out so that the API's tooling reads it (PRD NFR-SEC-07):

- A document of more than 1 MiB (1,048,576 bytes of UTF-8) of JSON, counted without the white space between its tokens and with each escape in a string, as `\"` or a `\u` escape, counted as the bytes of the UTF-8 of the character that it stands for, and an escaped surrogate pair as the one character that it stands for, so that whether a document is within the limit depends neither on how it is laid out, as PostgreSQL's `jsonb` lays it out, with a space after each `:` and `,`, nor on which characters its encoder escapes, as Go's `json.Marshal` escapes `<`, `>`, and `&`, and Python's `json.dumps` every character beyond ASCII; or whose canonical form, below, is longer than 1 MiB, which it can be when the JSON writes a number shorter, as `9e5` for 900000, so that every document that the server accepts can be written and read again.
- A document whose texts hold more than 100,000 characters together, counted as code points.
- Lists nested more than 5 deep: a list in no list is at depth 1.
- `attrs` on a `bulletList`.
- A list item that does not start with a paragraph.
- A mark twice, as two links, or the code mark with another.
- A link that the rules above refuse beyond the schema's pattern, which checks its scheme and refuses ASCII white space and control characters: one with white space beyond ASCII or a format character, a `%` that starts no escape, an authority without a host or with a character that it may not hold, as an `@` or a character beyond ASCII, a name with an empty label or that a browser reads as an IPv4 address that is not one, brackets that hold no IPv6 address, a port that is not a number or is above 65,535, or a `mailto:` link with a `#`, a field other than those above, or an escaped control character outside its body.
- A string that escapes a lone surrogate, as `"a\ud800b"`, which is no character and which no UTF-8 holds. `JSON.parse` keeps it in the string, where Go would read U+FFFD, so the server refuses the document with `invalid_json`, and so does the client.

The vectors mark each document of these that the schema accepts with `schema_accepts`.

### The canonical form

The server stores a document in its canonical form, which it is the same however it was sent:

- No field holds its default: no empty `content` or `marks`, and no `attrs` of an ordered list that starts at 1.
- An inline node's marks are in alphabetical order: `bold`, `code`, `italic`, `link`, `strike`.
- A paragraph's neighbouring texts with the same marks are one text, as ProseMirror joins them.
- An ID is in its canonical spelling.
- Every object's keys are sorted, and HTML is not escaped.

### Errors

The server refuses a document with a reason and a JSON Pointer (RFC 6901) to the value that is wrong, within the document. When a field is missing, the pointer is the object that misses it; when a document is too large, the pointer is the document's, `""`. A document with more than one thing wrong is refused at one of them, which implementations need not agree on: the server checks in an order of its own, and of several fields that an object may not have it names the first in the order of their code points, which is not the order of JavaScript's `sort`, of UTF-16 code units. So each invalid vector has one thing wrong.

| Reason | Meaning |
| --- | --- |
| `invalid_json` | The input is not one JSON value in UTF-8, or one of its strings escapes a lone surrogate. |
| `wrong_type` | A value has another JSON type than the schema's, as a string for an array, `null`, or a number that is not an integer. |
| `unknown_node` | A node's `type` is not one of the schema's. |
| `unknown_mark` | A mark's `type` is not one of the schema's. |
| `unknown_field` | An object has a field that the schema does not give it. |
| `missing_field` | A field that the schema requires is missing. |
| `content` | A node is where its parent may not hold it, as a list in a paragraph or a list item that starts with a list, or a content that must hold a node is empty. |
| `empty_text` | A text's `text` is empty. |
| `control_character` | A text holds a control character or a line or paragraph separator. |
| `duplicate_mark` | A node has a mark twice. |
| `excluded_mark` | The code mark is combined with another. |
| `link` | A link's URL is not an allowed one. |
| `reference` | An ID is not one, or of another type than it must be, or a value names an attribute of a variable, or none of a component. |
| `out_of_range` | An integer is outside its range. |
| `too_deep` | Lists nest more than 5 deep. |
| `too_large` | The document has more than 1 MiB of JSON, without the white space between its tokens and with its escapes counted as the characters that they stand for, or in its canonical form, or 100,000 characters of text. |

## Segments

The published format holds a document as its segments, which a runtime walks instead of the document's tree (PRD §6.1): a node's `text` and a connection's `label` are `{"segments": [...]}`, and a node's text also has a `preview`. The segments are the document's inline nodes in order, each with its marks, and segments that mark where its paragraphs, list items, and line breaks begin. A paragraph segment begins each paragraph but the document's first and the first of each list item, which an item segment begins instead. So a document of one paragraph has no segment that is not inline, and an empty document has no segment at all, and nor does a document's empty first paragraph.

| `kind` | Fields | Is |
| --- | --- | --- |
| `text` | `marks`, `value`, `href` | Text. |
| `mention` | `marks`, `ref`, `value`, `href` | A mention of component `ref`, whose name, when the snapshot was published, is `value`. |
| `value` | `marks`, `ref`, `attribute`, `decimals`, `name`, `href` | A value, of variable `ref`, or of the attribute `attribute` of component `ref`, shown with `decimals` when it has them. `name` names it as an expression does, as `met_elara` or `elara.trust`, for debugging, as an expression's AST names what it refers to. |
| `break` | — | A line break. |
| `paragraph` | `depth` | A paragraph begins. It belongs to the list item at `depth`, or to none when there is no `depth`. |
| `item` | `depth`, `list`, `number` | A list item begins: an item of a `bullet` or an `ordered` list, at `depth`, from 1, and an ordered list's item has its `number`. |

`marks` lists an inline segment's marks in alphabetical order, and is there, empty, when it has none. `href` is the URL of the link that an inline segment is part of. A `hardBreak`'s marks are in no segment.

```json
[
  { "kind": "text", "marks": [], "value": "Pack:" },
  { "depth": 1, "kind": "item", "list": "bullet" },
  { "kind": "text", "marks": ["bold"], "value": "Rope" },
  { "depth": 2, "kind": "item", "list": "ordered", "number": 3 },
  { "kind": "text", "marks": [], "value": "Oil" },
  { "kind": "paragraph" },
  { "kind": "text", "marks": [], "value": "Then go." }
]
```

## Plain text, preview, words, and hash

The text export (FR-TEXT-05) and the writing statistics (FR-TEXT-06) read a document's plain text, derived from its segments:

- **Plain text.** The texts and the mentions' names as they are, and each value as its `name` in braces, as `{met_elara}`. Each `paragraph` and `break` segment is a line feed, and so is each `item` segment but one that is the first segment. So a document that starts with a list starts with its first item, as does one whose empty first paragraph a list follows, since its segments are the same; but a document whose first segment is a `break`, or a `paragraph` after an empty first paragraph, starts with a line feed. An item segment then writes two spaces for each list that holds its list, and `• ` (U+2022 and a space), or its number and `. `. A `paragraph` or `break` segment within a list item then writes two spaces for each depth, which is as far as a bulleted item's text but not always as far as a numbered one's: `9. a`, a line break, and `b` are `9. a\n  b`. The segments above are `Pack:\n• Rope\n  3. Oil\nThen go.`.
- **Preview.** The plain text with each run of white space written as one space and none around it. When that is longer than 100 code points, the preview is its first 100, cut back to the last space among them when the cut falls within a word, without the spaces at its end, and an ellipsis (`…`, U+2026).
- **Words.** The words of the plain text without the bullets and numbers of its list items. A word is a run of characters that are not white space (Unicode's `White_Space`) with at least one letter or number among them (Unicode's `L` and `N`), so a dash between spaces is no word, and a run of Chinese or Japanese without spaces is one.
- **Hash.** `sha256:` and the lowercase hexadecimal SHA-256 of the plain text's UTF-8.

## The vectors

| Key | Contents |
| --- | --- |
| `names` | The names that the derivations show: each component's in `components`, and each value's in `values`, a variable's by its `ref`, and an attribute's by its `ref` and `attribute`. |
| `valid` | Documents that the server accepts, with their canonical form in `canonical` when it is not the document itself, their `segments`, and their plain `text`, `preview`, `words`, and `hash`. A canonical form, a document that is its own, and the segments are written as the server writes them, with each object's keys sorted and HTML not escaped, so that an implementation compares the bytes that it writes with theirs, once the white space between their tokens is taken out. |
| `invalid` | Documents that the server refuses, with the `reason` and the `pointer` of the error, and `schema_accepts` when the JSON Schema accepts them. Each has one thing wrong with it, so that every implementation reports the same one. |
| `words` | Texts and their number of words. |
| `previews` | Plain texts and their previews. |

A `note` says what a vector is for. The hashes were computed independently of the Go implementation, with Python's `hashlib`. The limits on a document's size are tried by the Go tests rather than by vectors, which would need a megabyte of JSON each.
