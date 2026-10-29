// SHA-256 (FIPS 180-4), with which load hashes a snapshot's files where the
// platform's, WebCrypto's crypto.subtle, is not there: in a page that is no
// secure context, as that of a self-hosted instance served over plain HTTP,
// on which everything but offline use works (PRD FR-OFF-01 AC4).

// The round constants: the first 32 bits of the fractional parts of the cube
// roots of the first 64 primes.
const k = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

/**
 * Returns the SHA-256 of bytes, its 32 bytes, as WebCrypto's digest of
 * "SHA-256" returns them. It copies no more of bytes than its last block.
 */
export function sha256(bytes: Uint8Array): Uint8Array {
  // The first 32 bits of the fractional parts of the square roots of the
  // first 8 primes.
  const h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
    0x1f83d9ab, 0x5be0cd19,
  ]);
  const w = new Uint32Array(64);
  const n = bytes.length;
  const whole = n - (n % 64);
  for (let at = 0; at < whole; at += 64) {
    compress(h, w, bytes, at);
  }
  // The rest of the message, a 1 bit, the zeros that fill its last block
  // but 8 bytes, and its length in bits in those 8, big-endian.
  const tail = new Uint8Array(n % 64 < 56 ? 64 : 128);
  tail.set(bytes.subarray(whole));
  tail[n % 64] = 0x80;
  const end = new DataView(tail.buffer);
  end.setUint32(tail.length - 8, Math.floor(n / 0x20000000));
  end.setUint32(tail.length - 4, (n * 8) >>> 0);
  for (let at = 0; at < tail.length; at += 64) {
    compress(h, w, tail, at);
  }
  const sum = new Uint8Array(32);
  const out = new DataView(sum.buffer);
  for (let i = 0; i < 8; i++) {
    out.setUint32(i * 4, h[i]);
  }
  return sum;
}

// compress folds the block of 64 bytes of p at at into the hash h, with w
// for its message schedule.
function compress(
  h: Uint32Array,
  w: Uint32Array,
  p: Uint8Array,
  at: number,
): void {
  for (let i = 0; i < 16; i++) {
    const j = at + i * 4;
    w[i] = (p[j] << 24) | (p[j + 1] << 16) | (p[j + 2] << 8) | p[j + 3];
  }
  for (let i = 16; i < 64; i++) {
    const x = w[i - 15];
    const y = w[i - 2];
    const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
    const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
    // A Uint32Array keeps the sum modulo 2^32.
    w[i] = w[i - 16] + s0 + w[i - 7] + s1;
  }
  let a = h[0];
  let b = h[1];
  let c = h[2];
  let d = h[3];
  let e = h[4];
  let f = h[5];
  let g = h[6];
  let hh = h[7];
  for (let i = 0; i < 64; i++) {
    const s1 =
      ((e >>> 6) | (e << 26)) ^
      ((e >>> 11) | (e << 21)) ^
      ((e >>> 25) | (e << 7));
    const ch = (e & f) ^ (~e & g);
    const t1 = (hh + s1 + ch + k[i] + w[i]) | 0;
    const s0 =
      ((a >>> 2) | (a << 30)) ^
      ((a >>> 13) | (a << 19)) ^
      ((a >>> 22) | (a << 10));
    const maj = (a & b) ^ (a & c) ^ (b & c);
    const t2 = (s0 + maj) | 0;
    hh = g;
    g = f;
    f = e;
    e = (d + t1) | 0;
    d = c;
    c = b;
    b = a;
    a = (t1 + t2) | 0;
  }
  h[0] += a;
  h[1] += b;
  h[2] += c;
  h[3] += d;
  h[4] += e;
  h[5] += f;
  h[6] += g;
  h[7] += hh;
}
