import { act, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { createPortal } from "react-dom";
import { describe, expect, test } from "vitest";
import { useHeld } from "./held";

/** A held form of an entity, whose control opens a menu of its own in a portal, as Radix's does, outside the form's elements. */
function HeldForm({ entity }: { entity: { rev: number; name: string } }) {
  const held = useHeld(entity);
  const [open, setOpen] = useState(false);
  return (
    <>
      <form key={held.key} {...held.props}>
        <input aria-label="Name" defaultValue={held.shown.name} />
        <button type="button" onClick={() => setOpen(true)}>
          Options
        </button>
        {open &&
          createPortal(
            <div>
              <button type="button" onBlur={() => setOpen(false)}>
                Option
              </button>
            </div>,
            document.body,
          )}
      </form>
      <button type="button">Elsewhere</button>
    </>
  );
}

describe("a held form", () => {
  test("holds what it shows while the focus is in what one of its controls opened in a portal, as a menu, and lets go of it once the focus leaves", async () => {
    const { rerender } = render(<HeldForm entity={{ rev: 1, name: "Tide" }} />);
    const options = screen.getByRole("button", { name: "Options" });
    options.focus();
    // Another session changes the entity while the user is in the form.
    rerender(<HeldForm entity={{ rev: 2, name: "Shore" }} />);
    options.click();
    const option = await screen.findByRole("button", { name: "Option" });
    option.focus();
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(options.isConnected).toBe(true);
    expect(
      (screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value,
    ).toBe("Tide");
    screen.getByRole("button", { name: "Elsewhere" }).focus();
    await waitFor(() =>
      expect(
        (screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement)
          .value,
      ).toBe("Shore"),
    );
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Elsewhere" }),
    );
  });

  test("shown anew, gives the focus back to the control that had it, though a toolbar before it, as a rich text's, is there no more", () => {
    let release = () => {};
    function ToolbarForm({
      entity,
    }: {
      entity: { rev: number; toolbar: boolean };
    }) {
      const held = useHeld(entity);
      release = held.release;
      return (
        <form key={held.key} {...held.props}>
          <input aria-label="Title" />
          {held.shown.toolbar && (
            <div role="toolbar">
              <button type="button">Bold</button>
              <button type="button">Italic</button>
            </div>
          )}
          {/* An editable text, as a rich text's, which may not be changed
              where it has no toolbar. */}
          <div contentEditable={held.shown.toolbar} />
          <input aria-label="Name" />
        </form>
      );
    }
    const { rerender } = render(
      <ToolbarForm entity={{ rev: 1, toolbar: true }} />,
    );
    screen.getByRole("textbox", { name: "Name" }).focus();
    rerender(<ToolbarForm entity={{ rev: 2, toolbar: false }} />);
    act(() => release());
    expect(document.activeElement).toBe(
      screen.getByRole("textbox", { name: "Name" }),
    );
  });

  test("shown anew while the focus is in what one of its controls opened in a portal, which goes with it, gives the focus back to that control", () => {
    let release = () => {};
    /** A control that opens a field of its own in a portal, as a rich text's link dialog, which the form shown anew is without. */
    function Opener() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            Link
          </button>
          {open && createPortal(<input aria-label="URL" />, document.body)}
        </>
      );
    }
    function PortalForm({ entity }: { entity: { rev: number } }) {
      const held = useHeld(entity);
      release = held.release;
      return (
        <form key={held.key} {...held.props}>
          <input aria-label="Title" />
          <Opener />
        </form>
      );
    }
    const { rerender } = render(<PortalForm entity={{ rev: 1 }} />);
    const link = screen.getByRole("button", { name: "Link" });
    link.focus();
    act(() => link.click());
    act(() => screen.getByRole("textbox", { name: "URL" }).focus());
    // A save ends, of the entity as it is now.
    rerender(<PortalForm entity={{ rev: 2 }} />);
    act(() => release());
    expect(screen.queryByRole("textbox", { name: "URL" })).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Link" }),
    );
  });
});
