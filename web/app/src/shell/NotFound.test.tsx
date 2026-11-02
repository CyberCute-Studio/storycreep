import { createRoute, notFound } from "@tanstack/react-router";
import { screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Route as rootRoute } from "../routes/__root";
import { renderApp } from "../test/app";

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
    const { unmount } = renderApp(path, { routes: routeTree });
    await screen.findByRole("heading", { level: 1, name: "Page not found" });
    expect(screen.getAllByRole("main"), path).toHaveLength(1);
    expect(screen.getAllByRole("banner"), path).toHaveLength(1);
    unmount();
  }
});
