// Whether the page travels unencrypted (PRD NFR-SEC-15): a self-hosted
// instance may be served over plain HTTP, so that it runs without
// certificates, but then it warns loudly, on every page, when it is reached
// at an address other than the machine's own, since its passwords, its
// session cookie, and everything else then cross the network as they are.
// The machine's own addresses never leave it, and browsers take them for
// secure contexts, as they do HTTPS.

/** The part of a page's location that tells how it travels. */
export interface PageLocation {
  protocol: string;
  hostname: string;
}

/**
 * Reports whether a page at location travels unencrypted over a network:
 * it is served over plain HTTP from a host that is not a loopback address
 * of the machine, as a LAN address is. The loopback hosts are those that
 * browsers take for secure contexts: localhost and its subdomains,
 * 127.0.0.0/8, and [::1].
 */
export function unencrypted(location: PageLocation): boolean {
  return location.protocol === "http:" && !loopback(location.hostname);
}

function loopback(hostname: string): boolean {
  const host = hostname.toLowerCase().replace(/\.$/, "");
  if (host === "localhost" || host.endsWith(".localhost") || host === "[::1]") {
    return true;
  }
  const octets = host.split(".");
  return (
    octets.length === 4 &&
    octets[0] === "127" &&
    octets.every((o) => /^\d{1,3}$/.test(o) && Number(o) <= 255)
  );
}
