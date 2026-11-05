// The URLs of the editor's links (PRD NFR-SEC-08): what a link may lead to,
// by the rules of schema/rich-text/README.md, and how the editor writes one
// that a user types or pastes so that the server takes it.

import { checkHref } from "@storycreep/shared/richtext";

/**
 * Returns href as a link of the editor stores it, or undefined for a URL
 * that no link may lead to (checkHref). An http or https URL whose host is
 * beyond ASCII has its host written as IDNA writes it (xn--), which is what
 * a browser leads to (new URL(href).hostname), and the rest of the URL kept
 * as it is; white space around the URL, as a pasted one has, is taken off.
 */
export function storedHref(href: string): string | undefined {
  let url = href.trim();
  const parts = /^(https?:\/\/)([^/?#]*)(.*)$/is.exec(url);
  if (parts !== null && /[^!-~]/u.test(parts[2])) {
    // An authority with userinfo is refused as it is, rather than read, and
    // so is one with a backslash, where a browser's host ends and its path
    // begins, as checkHref refuses one of ASCII.
    if (parts[2].includes("@") || parts[2].includes("\\")) {
      return undefined;
    }
    try {
      url = `${parts[1]}${new URL(url).host}${parts[3]}`;
    } catch {
      return undefined;
    }
  }
  return checkHref(url) === undefined ? url : undefined;
}

/**
 * Returns the URL that a user types into the link's field, as storedHref
 * writes it, or undefined for one that no link may lead to: an address
 * without a scheme, as example.com, is an https URL, and so is a host with a
 * port, as example.com:8080 or localhost:3000, whose host has a dot or is
 * localhost, rather than a URL of a scheme of that name; and one that is an
 * email address, as keeper@example.com, a mailto URL.
 */
export function typedHref(typed: string): string | undefined {
  const text = typed.trim();
  if (text === "") {
    return undefined;
  }
  if (
    /^[a-z][a-z0-9+.-]*:/i.test(text) &&
    !/^([^:]*\.[^:]*|localhost):\d+([/?#]|$)/i.test(text)
  ) {
    return storedHref(text);
  }
  if (/^[^\s@/?#]+@[^\s@/?#]+$/u.test(text)) {
    return storedHref(`mailto:${text}`);
  }
  return storedHref(`https://${text}`);
}
