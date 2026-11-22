import { createI18n } from "@storycreep/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { I18nextProvider } from "react-i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createStore } from "zustand/vanilla";
import { api } from "../api";
import type { DocumentState, ProjectDocument } from "../document/store";
import { refused } from "../test/fake";
import { TextExport } from "./TextExport";

// The form of the text export (PRD FR-TEXT-05): what it asks the server for,
// of the draft or of a release, of the project or of a board, in each
// format; the name that the file is saved under; and a refusal, in words.

const boards = [
  { id: "bd_a", name: "Quay" },
  { id: "bd_b", name: "Attic" },
];

let saved: { name: string; href: string }[];

beforeEach(() => {
  saved = [];
  vi.stubGlobal(
    "URL",
    Object.assign(URL, {
      createObjectURL: vi.fn(() => "blob:file"),
      revokeObjectURL: vi.fn(),
    }),
  );
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    saved.push({ name: this.download, href: this.href });
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

/** The server's answer of a file named name. */
function file(name: string) {
  return {
    status: 200,
    body: new Blob(["x"]),
    response: new Response(null, {
      headers: { "Content-Disposition": `attachment; filename=${name}` },
    }),
  } as never;
}

async function wrapper() {
  const i18n = await createI18n("en");
  const client = new QueryClient();
  return ({ children }: { children: ReactNode }) => (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </I18nextProvider>
  );
}

describe("the text export's form", () => {
  it("downloads the draft's whole project as CSV by default, under the server's name", async () => {
    const call = vi
      .spyOn(api, "call")
      .mockResolvedValue(file("harbour-draft-text.csv"));
    render(<TextExport projectId="prj_a" boards={boards} />, {
      wrapper: await wrapper(),
    });
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(call).toHaveBeenCalledWith("exportDraftText", {
      path: { project_id: "prj_a" },
      query: { format: "csv" },
    });
    expect(saved[0].name).toBe("harbour-draft-text.csv");
  });

  it("asks for a release's board, as JSON", async () => {
    const call = vi
      .spyOn(api, "call")
      .mockResolvedValue(file("harbour-1.0.0-attic-text.json"));
    render(
      <TextExport
        projectId="prj_a"
        release={{ id: "sn_a", version: "1.0.0" }}
        boards={boards}
      />,
      { wrapper: await wrapper() },
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Export" }),
      "bd_b",
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Format" }),
      "json",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(call).toHaveBeenCalledWith("exportSnapshotText", {
      path: { project_id: "prj_a", snapshot_id: "sn_a" },
      query: { format: "json", board: "bd_b" },
    });
  });

  it("no longer asks for a board that the list has lost", async () => {
    const call = vi
      .spyOn(api, "call")
      .mockResolvedValue(file("harbour-draft-text.csv"));
    const Wrapper = await wrapper();
    const { rerender } = render(
      <Wrapper>
        <TextExport projectId="prj_a" boards={boards} />
      </Wrapper>,
    );
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Export" }),
      "bd_b",
    );
    rerender(
      <Wrapper>
        <TextExport projectId="prj_a" boards={boards.slice(0, 1)} />
      </Wrapper>,
    );
    expect(screen.getByRole("combobox", { name: "Export" })).toHaveProperty(
      "value",
      "",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(call).toHaveBeenCalledWith("exportDraftText", {
      path: { project_id: "prj_a" },
      query: { format: "csv" },
    });
  });

  it("does not choose a board again when it comes back to the list", async () => {
    vi.spyOn(api, "call").mockResolvedValue(file("harbour-draft-text.csv"));
    const Wrapper = await wrapper();
    const form = (list: typeof boards) => (
      <Wrapper>
        <TextExport projectId="prj_a" boards={list} />
      </Wrapper>
    );
    const { rerender } = render(form(boards));
    const scope = screen.getByRole("combobox", { name: "Export" });
    await userEvent.selectOptions(scope, "bd_b");
    rerender(form(boards.slice(0, 1)));
    rerender(form(boards));
    expect(scope).toHaveProperty("value", "");
  });

  it("keeps a refusal whose board has left the list, as it says why no file came", async () => {
    vi.spyOn(api, "call").mockRejectedValue(
      refused(404, { code: "not_found" }),
    );
    const Wrapper = await wrapper();
    const form = (list: typeof boards) => (
      <Wrapper>
        <TextExport projectId="prj_a" boards={list} />
      </Wrapper>
    );
    const { rerender } = render(form(boards));
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Export" }),
      "bd_b",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    const alert = screen.getByRole("alert");
    await waitFor(() => expect(alert.textContent).not.toBe(""));
    rerender(form(boards.slice(0, 1)));
    expect(screen.getByRole("alert").textContent).not.toBe("");
  });

  it("reads the draft once the page's batches have been answered", async () => {
    const call = vi
      .spyOn(api, "call")
      .mockResolvedValue(file("harbour-draft-text.csv"));
    const store = createStore<Pick<DocumentState, "sending">>(() => ({
      sending: 1,
    }));
    render(
      <TextExport
        projectId="prj_a"
        doc={{ store } as unknown as ProjectDocument}
        boards={boards}
      />,
      { wrapper: await wrapper() },
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    expect(call).not.toHaveBeenCalled();
    store.setState({ sending: 0 });
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(call).toHaveBeenCalledTimes(1);
  });

  it("says why the server refused it", async () => {
    vi.spyOn(api, "call").mockRejectedValue(
      refused(429, { code: "rate_limited" }),
    );
    render(<TextExport projectId="prj_a" boards={boards} />, {
      wrapper: await wrapper(),
    });
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(saved).toHaveLength(0);
  });

  it("holds its choices while a download is on its way, so that a refusal is of what they show", async () => {
    let refuse: (error: unknown) => void = () => {};
    vi.spyOn(api, "call").mockReturnValue(
      new Promise((_, reject) => {
        refuse = reject;
      }),
    );
    render(<TextExport projectId="prj_a" boards={boards} />, {
      wrapper: await wrapper(),
    });
    const scope = screen.getByRole("combobox", { name: "Export" });
    const format = screen.getByRole("combobox", { name: "Format" });
    await userEvent.selectOptions(scope, "bd_b");
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    await waitFor(() => expect(scope).toHaveProperty("disabled", true));
    expect(format).toHaveProperty("disabled", true);
    refuse(refused(404, { code: "not_found" }));
    const alert = screen.getByRole("alert");
    await waitFor(() => expect(alert.textContent).not.toBe(""));
    expect(scope).toHaveProperty("disabled", false);
    expect(scope).toHaveProperty("value", "bd_b");
  });

  it("drops a refusal once the form asks for something else", async () => {
    vi.spyOn(api, "call").mockRejectedValue(
      refused(404, { code: "not_found" }),
    );
    render(<TextExport projectId="prj_a" boards={boards} />, {
      wrapper: await wrapper(),
    });
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Export" }),
      "bd_b",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Download text" }),
    );
    // The alert is always there, as a live region, and empty without a refusal.
    const alert = screen.getByRole("alert");
    await waitFor(() => expect(alert.textContent).not.toBe(""));
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Export" }),
      "",
    );
    await waitFor(() => expect(alert.textContent).toBe(""));
  });
});
