# Rich text

The rich text of a project's content, node bodies, connection labels, and component descriptions, is a ProseMirror document of a constrained schema, stored as JSON (PRD FR-TEXT-01, ADR-020, plan note N40). The schema's nodes and marks are TipTap's, so that the editor's extensions write the documents that the server stores.

| File | Contents |
| --- | --- |
| [`v1/rich-text.schema.json`](v1/rich-text.schema.json) | The JSON Schema of a document, version 1, with examples. Its `$id` is the URL of the file on the main branch. `server/internal/richtext` generates it, and `go test ./...` fails when it is out of date; `go test ./internal/richtext -run TestSchemaFile -update`, in `server/`, rewrites it. |
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

The marks are `bold`, `italic`, `strike`, `code`, and `link`, whose `attrs.href` is the URL. A node has each mark at most once, and the code mark with no other, as the editor's code mark excludes the others. Every node and mark is an object with a `type`, and has no field that the table does not give it, so `attrs` on a paragraph, or a link's `target`, is refused. An ID is read regardless of case, as [`../ids/README.md`](../ids/README.md) says.

A link is an absolute `http` or `https` URL with a host, or a `mailto` URL with an address, of at most 2,048 characters, none of them white space (Unicode's `White_Space`), a control character, or U+FEFF (PRD NFR-SEC-08). `javascript:`, `data:`, `file:`, and relative URLs are refused.

### Limits and what the server checks beyond the schema

Every document that the server accepts validates against the JSON Schema. The server also refuses what JSON Schema does not express (PRD NFR-SEC-07):

- A document of more than 1 MiB of JSON, or whose texts hold more than 100,000 characters together, counted as code points.
- Lists nested more than 5 deep: a list in no list is at depth 1.
- A list item that does not start with a paragraph.
- A mark twice, as two links, or the code mark with another.
- A link that is not a URL, as one with a `%` that starts no escape, or with white space beyond ASCII.

The vectors mark each document of these that the schema accepts with `schema_accepts`.

### The canonical form

The server stores a document in its canonical form, which it is the same however it was sent:

- No field holds its default: no empty `content` or `marks`, and no `attrs` of an ordered list that starts at 1.
- An inline node's marks are in alphabetical order: `bold`, `code`, `italic`, `link`, `strike`.
- A paragraph's neighbouring texts with the same marks are one text, as ProseMirror joins them.
- An ID is in its canonical spelling.
- Every object's keys are sorted, and HTML is not escaped.

### Errors

The server refuses a document with a reason and a JSON Pointer (RFC 6901) to the value that is wrong, within the document. When a field is missing, the pointer is the object that misses it; when a document is too large, the pointer is the document's, `""`.

| Reason | Meaning |
| --- | --- |
| `invalid_json` | The input is not one JSON value in UTF-8. |
| `wrong_type` | A value has another JSON type than the schema's, as a string for an array, or a number that is not an integer. |
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
| `too_large` | The document has more than 1 MiB of JSON or 100,000 characters of text. |

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

- **Plain text.** The texts and the mentions' names as they are, and each value as its `name` in braces, as `{met_elara}`. Each segment that begins a paragraph, a list item, or a line break, but the first segment, is a line feed. An item segment then writes two spaces for each list that holds its list, and `• ` (U+2022 and a space), or its number and `. `. A paragraph or a line break within a list item is indented as far as the item's text: two spaces for each depth. The segments above are `Pack:\n• Rope\n  3. Oil\nThen go.`.
- **Preview.** The plain text with each run of white space written as one space and none around it. When that is longer than 100 code points, the preview is its first 100, cut back to the last space among them when the cut falls within a word, without the spaces at its end, and an ellipsis (`…`, U+2026).
- **Words.** The words of the plain text without the bullets and numbers of its list items. A word is a run of characters that are not white space (Unicode's `White_Space`) with at least one letter or number among them (Unicode's `L` and `N`), so a dash between spaces is no word, and a run of Chinese or Japanese without spaces is one.
- **Hash.** `sha256:` and the lowercase hexadecimal SHA-256 of the plain text's UTF-8.

## The vectors

| Key | Contents |
| --- | --- |
| `names` | The names that the derivations show: each component's in `components`, and each value's in `values`, a variable's by its `ref`, and an attribute's by its `ref` and `attribute`. |
| `valid` | Documents that the server accepts, with their canonical form in `canonical` when it is not the document itself, their `segments`, and their plain `text`, `preview`, `words`, and `hash`. |
| `invalid` | Documents that the server refuses, with the `reason` and the `pointer` of the error, and `schema_accepts` when the JSON Schema accepts them. Each has one thing wrong with it, so that every implementation reports the same one. |
| `words` | Texts and their number of words. |
| `previews` | Plain texts and their previews. |

A `note` says what a vector is for. The hashes were computed independently of the Go implementation, with Python's `hashlib`. The limits on a document's size are tried by the Go tests rather than by vectors, which would need a megabyte of JSON each.
