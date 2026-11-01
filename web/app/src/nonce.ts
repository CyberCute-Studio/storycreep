// The nonce of the page's Content-Security-Policy (PRD NFR-SEC-09), which
// the server writes into each response of the entry page, as the nonce of
// a meta element that Vite adds (vite.config.ts). The styles that a library
// adds to the page itself need it, or the policy refuses them.

/** Returns the page's nonce, or undefined if it has none. */
export function cspNonce(doc: Document): string | undefined {
  const meta = doc.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]');
  // A browser hides a nonce from getAttribute, but not from the property.
  const nonce = meta?.nonce || meta?.getAttribute("nonce");
  return nonce || undefined;
}
