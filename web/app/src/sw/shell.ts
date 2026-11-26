// What the app's service worker (sw.ts) does with the tool's entry page,
// the app's shell, which it keeps in its cache (PRD FR-OFF-01, plan note
// N95): which navigations it answers with the shell, as the server would
// answer them with its entry page, and how each answer gets a nonce of its
// own, as each of the server's does, rather than the one of the response
// that it cached. These need nothing of a service worker, so that the
// tests run them as they are.

/** Where the server serves the app's files, and the service worker's scope. */
export const appPath = "/app/";

/**
 * The directory of the build where Vite writes the files whose names hold a
 * hash of their content (assetsDir, in vite.config.ts), which the server
 * serves as files, never as the entry page.
 */
const assetsDir = "assets";

/**
 * Reports whether the server answers a navigation to url, of the origin,
 * with the app's entry page, as registerPages in
 * server/internal/httpserver/pages.go does: at /app/, and at every path
 * under it that is valid, outside assets/, and whose last segment has no
 * extension, so that the app's router shows the page; not at
 * /app/index.html, which it redirects to /app/, nor at a path that names a
 * file, as a file of an older build does, which it answers with not_found.
 * The service worker answers the others as the server does: it passes them
 * on.
 */
export function answersWithShell(url: URL, origin: string): boolean {
  if (url.origin !== origin || !url.pathname.startsWith(appPath)) {
    return false;
  }
  // The server reads the path decoded, as %2E as a dot.
  let name: string;
  try {
    name = decodeURIComponent(url.pathname.slice(appPath.length));
  } catch {
    // Not UTF-8, which no valid path of the server's is.
    return false;
  }
  if (name === "") {
    return true;
  }
  if (name === "index.html") {
    return false;
  }
  if (!validPath(name.endsWith("/") ? name.slice(0, -1) : name)) {
    return false;
  }
  if (name === assetsDir || name.startsWith(`${assetsDir}/`)) {
    return false;
  }
  // path.Ext(path.Base(name)) in Go: the last segment, without the slashes
  // that end the path, holds a dot.
  const segments = name.split("/").filter((s) => s !== "");
  return !(segments.at(-1) ?? ".").includes(".");
}

/**
 * Reports whether name is a valid path of Go's io/fs (fs.ValidPath): ".",
 * or segments separated by slashes, none of them empty, ".", or "..".
 */
function validPath(name: string): boolean {
  return (
    name === "." ||
    name.split("/").every((s) => s !== "" && s !== "." && s !== "..")
  );
}

/** A nonce's source in a Content-Security-Policy, with the nonce. */
const nonceSource = /'nonce-([^']+)'/;

/** Returns the nonce that a response's Content-Security-Policy allows, if it names one. */
export function nonceOf(response: Response): string | undefined {
  return response.headers
    .get("Content-Security-Policy")
    ?.match(nonceSource)?.[1];
}

/** The letters of a nonce, those of base32 (RFC 4648), as the server's. */
const nonceLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

/**
 * Returns a new nonce: 130 random bits as 26 letters of base32, as the
 * server's (crypto/rand.Text). Each letter takes 5 bits of a random byte,
 * of which 256 values are as many for each of the 32 letters.
 */
export function newNonce(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(26));
  return Array.from(bytes, (b) => nonceLetters[b & 31]).join("");
}

/**
 * Returns the shell, a response of the entry page whose body is body, with
 * nonce in place of the response's own nonce, in its body, which holds it
 * as the nonce of the page's scripts and styles, and of the meta element
 * that the app reads it from, and in its Content-Security-Policy, which
 * allows the scripts and styles of that nonce alone. A response whose
 * policy names no nonce is answered with its body as it is. The headers
 * that described the body as it was sent, its length and its encoding,
 * describe none of a body made anew, and go.
 */
export function withNonce(
  response: Response,
  body: string,
  nonce: string,
): Response {
  const headers = new Headers(response.headers);
  headers.delete("Content-Length");
  headers.delete("Content-Encoding");
  const old = nonceOf(response);
  if (old !== undefined) {
    const policy = headers.get("Content-Security-Policy") ?? "";
    headers.set(
      "Content-Security-Policy",
      policy.replaceAll(`'nonce-${old}'`, `'nonce-${nonce}'`),
    );
    body = body.replaceAll(old, nonce);
  }
  return new Response(body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

/**
 * What a response's body holds where it holds its nonce, once shellOf has
 * taken that out: the placeholder of the build's index.html, which the
 * server replaces with each response's nonce (cspNonce, in vite.config.ts).
 */
const noncePlace = "__STORYCREEP_CSP_NONCE__";

/**
 * Returns the body of a response of the entry page, body, without its
 * nonce: what two of the server's responses of the same build hold alike,
 * since each response has a nonce of its own and nothing else of its own,
 * which is the build's index.html.
 */
export function shellOf(response: Response, body: string): string {
  const nonce = nonceOf(response);
  return nonce === undefined ? body : body.replaceAll(nonce, noncePlace);
}
