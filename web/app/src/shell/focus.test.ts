import { describe, expect, it, onTestFinished } from "vitest";
import { CONTENT_ID, keepFocusOnPage } from "./focus";

// keepFocusOnPage on a page of its own: a frame with a header and the
// content, which has a list's item with a button, and a dialog's button
// outside the frame, as Radix puts it.

/** Lets the page's observer and what it queued run. */
const settle = () => new Promise((resolve) => setTimeout(resolve));

/** Shows the page, and returns its element of id. */
function page(): (id: string) => HTMLElement {
  document.body.innerHTML = `
    <div id="frame">
      <header><button id="sign-out">Sign out</button></header>
      <main id="${CONTENT_ID}" tabindex="-1">
        <ul><li id="item"><button id="revoke">Revoke</button></li></ul>
        <button id="more">Show more</button>
      </main>
    </div>
    <div id="dialog"><button id="confirm">Revoke the token</button></div>`;
  const get = (id: string) => document.getElementById(id) as HTMLElement;
  const stop = keepFocusOnPage(get("frame"));
  onTestFinished(() => {
    stop();
    document.body.innerHTML = "";
  });
  return get;
}

/**
 * Disables button as Chromium does one that has the focus, which moves the
 * focus to the document's body, and blurs the button, disabled: jsdom moves
 * the focus of an enabled control alone.
 */
function disable(button: HTMLElement) {
  button.blur();
  (button as HTMLButtonElement).disabled = true;
  button.dispatchEvent(
    new FocusEvent("focusout", { bubbles: true, relatedTarget: null }),
  );
}

describe("keepFocusOnPage", () => {
  // Verifies: NFR-A11Y-04
  it("gives the page's content the focus once the control that has it goes", async () => {
    const get = page();
    get("revoke").focus();
    get("item").remove();
    await settle();
    expect(document.activeElement).toBe(get(CONTENT_ID));
  });

  // Verifies: NFR-A11Y-04
  it("gives the focus back to a control that lost it as it was disabled, once it is enabled again", async () => {
    const get = page();
    get("more").focus();
    disable(get("more"));
    await settle();
    expect(document.activeElement).toBe(get(CONTENT_ID));
    (get("more") as HTMLButtonElement).disabled = false;
    await settle();
    expect(document.activeElement).toBe(get("more"));
  });

  // Verifies: NFR-A11Y-04
  it("keeps the content's focus once a control that was disabled goes", async () => {
    const get = page();
    get("revoke").focus();
    disable(get("revoke"));
    await settle();
    get("item").remove();
    await settle();
    expect(document.activeElement).toBe(get(CONTENT_ID));
  });

  it("leaves the focus where the user moved it", async () => {
    const get = page();
    get("more").focus();
    disable(get("more"));
    await settle();
    get("sign-out").focus();
    (get("more") as HTMLButtonElement).disabled = false;
    await settle();
    expect(document.activeElement).toBe(get("sign-out"));

    // A click outside any control leaves it nowhere, for what goes after.
    get("revoke").focus();
    get("revoke").blur();
    get("more").remove();
    await settle();
    expect(document.activeElement).toBe(document.body);
  });

  // Verifies: NFR-A11Y-04
  it("leaves a dialog's focus to the dialog, and gives the content it once the dialog closes without the control that opened it", async () => {
    const get = page();
    get("revoke").focus();
    // The dialog that the item's button opened.
    get("confirm").focus();
    get("item").remove();
    await settle();
    expect(document.activeElement).toBe(get("confirm"));

    get("dialog").remove();
    await settle();
    expect(document.activeElement).toBe(get(CONTENT_ID));
  });

  // Verifies: NFR-A11Y-04
  it("gives a dialog's control that lost the focus as it was disabled the focus back once it is enabled again, and the page's content none", async () => {
    const get = page();
    get("revoke").focus();
    // The confirmation that the item's button opened, whose button is
    // disabled while its request is answered.
    get("confirm").focus();
    disable(get("confirm"));
    await settle();
    expect(document.activeElement).toBe(document.body);
    // The request was refused.
    (get("confirm") as HTMLButtonElement).disabled = false;
    await settle();
    expect(document.activeElement).toBe(get("confirm"));
  });

  it("leaves the focus where the browser puts it before the user has moved it", async () => {
    const get = page();
    get("item").remove();
    await settle();
    expect(document.activeElement).toBe(document.body);
  });
});
