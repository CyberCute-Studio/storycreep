import { createI18n } from "@storycreep/shared/i18n";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRoute,
  createRouter,
  notFound,
  RouterProvider,
} from "@tanstack/react-router";
import { render, screen } from "@testing-library/react";
import { I18nextProvider } from "react-i18next";
import { afterEach, expect, it, vi } from "vitest";
import { ErrorConsoleProvider } from "../errors/ErrorConsole";
import { Route as rootRoute } from "../routes/__root";

// The page not found of the app's root route, which the router shows in the
// route's Outlet, within the Shell, for a URL that no route matches and for
// a route's loader that says that it has no page, but in place of the Shell
// for a component that says so as it renders.

afterEach(() => {
  vi.restoreAllMocks();
});

function Missing(): never {
  throw notFound();
}

const routeTree = rootRoute.addChildren([
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/loaded",
    loader: () => {
      throw notFound();
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/rendered",
    component: Missing,
  }),
]);

it("shows the page not found within the Shell, once, wherever the router finds no page", async () => {
  // React reports the error that a boundary catches, and jsdom has no
  // scrolling, which the router resets.
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  for (const path of ["/nowhere", "/loaded", "/rendered"]) {
    const queryClient = new QueryClient();
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: [path] }),
      context: { queryClient },
      disableGlobalCatchBoundary: true,
    });
    const { unmount } = render(
      <I18nextProvider i18n={createI18n("en")}>
        <QueryClientProvider client={queryClient}>
          <ErrorConsoleProvider>
            <RouterProvider router={router} />
          </ErrorConsoleProvider>
        </QueryClientProvider>
      </I18nextProvider>,
    );
    await screen.findByRole("heading", { level: 1, name: "Page not found" });
    expect(screen.getAllByRole("main"), path).toHaveLength(1);
    expect(screen.getAllByRole("banner"), path).toHaveLength(1);
    unmount();
  }
});
