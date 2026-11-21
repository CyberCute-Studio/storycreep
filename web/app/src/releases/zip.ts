// Reads the zip of a release's files, as the server's export writes it
// (PRD FR-SNAP-02, plan note N88), so that the read-only view of a release
// shows what the release's download holds, byte for byte, with the
// browser's own DecompressionStream rather than a dependency: its central
// directory names each member, stored or deflated, as the server's are.

const endOfDirectory = 0x06054b50;
const directoryEntry = 0x02014b50;
const localHeader = 0x04034b50;
/** How long the end of the central directory is, without its comment. */
const endLength = 22;
const stored = 0;
const deflated = 8;

/** Returns data inflated from raw Deflate. */
async function inflate(
  data: Uint8Array<ArrayBuffer>,
): Promise<Uint8Array<ArrayBuffer>> {
  const body = new Response(data).body;
  if (body === null) {
    throw new Error("zip: a member without a body");
  }
  const out = body.pipeThrough(new DecompressionStream("deflate-raw"));
  return new Uint8Array(await new Response(out).arrayBuffer());
}

/**
 * Returns the files of the zip of bytes, by their paths. It throws on what
 * it cannot read: no central directory, a member of a method other than
 * stored or Deflate, as a ZIP64 archive's sizes, which no export reaches, or
 * a member whose size is not the one that its entry says.
 */
export async function readZip(
  bytes: Uint8Array<ArrayBuffer>,
): Promise<Map<string, Uint8Array<ArrayBuffer>>> {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  // The end of the directory is the last thing in the file, but for a
  // comment of at most 65,535 bytes.
  let end = bytes.length - endLength;
  const earliest = Math.max(0, end - 0xffff);
  while (end >= earliest && view.getUint32(end, true) !== endOfDirectory) {
    end--;
  }
  if (end < earliest) {
    throw new Error("zip: no central directory");
  }
  const count = view.getUint16(end + 10, true);
  let at = view.getUint32(end + 16, true);
  const names = new TextDecoder();
  const files = new Map<string, Uint8Array<ArrayBuffer>>();
  for (let i = 0; i < count; i++) {
    if (view.getUint32(at, true) !== directoryEntry) {
      throw new Error("zip: a malformed central directory");
    }
    const method = view.getUint16(at + 10, true);
    const compressed = view.getUint32(at + 20, true);
    const size = view.getUint32(at + 24, true);
    const nameLength = view.getUint16(at + 28, true);
    const extraLength = view.getUint16(at + 30, true);
    const commentLength = view.getUint16(at + 32, true);
    const offset = view.getUint32(at + 42, true);
    const name = names.decode(bytes.subarray(at + 46, at + 46 + nameLength));
    at += 46 + nameLength + extraLength + commentLength;
    if (compressed === 0xffffffff || size === 0xffffffff) {
      throw new Error(`zip: ${name} needs ZIP64`);
    }
    if (view.getUint32(offset, true) !== localHeader) {
      throw new Error(`zip: ${name} has no local header`);
    }
    const start =
      offset +
      30 +
      view.getUint16(offset + 26, true) +
      view.getUint16(offset + 28, true);
    const data = bytes.slice(start, start + compressed);
    if (data.length !== compressed) {
      throw new Error(`zip: ${name} is cut short`);
    }
    let file: Uint8Array<ArrayBuffer>;
    if (method === stored) {
      file = data;
    } else if (method === deflated) {
      file = await inflate(data);
    } else {
      throw new Error(`zip: ${name} has the method ${method}`);
    }
    if (file.length !== size) {
      throw new Error(`zip: ${name} is not of its size`);
    }
    files.set(name, file);
  }
  return files;
}
