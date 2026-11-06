import { render, screen, waitFor } from "@testing-library/react";
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
});
