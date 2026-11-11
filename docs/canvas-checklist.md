# The canvas's checklist for the reference devices

PRD §9.1 holds the board canvas to targets on two reference devices, which CI's runners are not: its device metrics are checked by hand, once a release, with the scripted runs of this checklist. CI's canvas benchmark job runs the same scripts on every pull request, on a runner, and reports what they measure; it fails only on what a runner can tell (plan note N66). This page is how a release's tester checks the rest.

| Device | What it is (PRD §9.1) |
| --- | --- |
| Reference tablet | A mid-range Android tablet, Snapdragon 695-class, 4 GB of RAM, 1920×1200, in Chrome stable, as a Galaxy Tab A9+. |
| Reference desktop | A 4-core laptop of 2019 or later, in Chrome, Firefox, or Safari stable. |

## 1. Set up

1. Run an instance that the tablet reaches over the LAN, as the development compose file's server does at the machine's LAN address (`deploy/README.md`), with an account of its own for the checklist. Plain HTTP is fine on a LAN; the app says so in its warning.
2. Load the fixtures and the boards that the runs measure, from a checkout of the repository on a machine that reaches the instance, as the benchmark's setup loads them in CI:

   ```bash
   STORYCREEP_BENCH_BASE_URL=http://192.168.1.20:8080 STORYCREEP_BENCH_EMAIL=checklist@example.com STORYCREEP_BENCH_PASSWORD='…' STORYCREEP_BENCH_SEED=1 pnpm --filter @storycreep/app exec playwright test -c playwright.bench.config.ts --project setup
   ```

   It loads `medium`, `dense`, and `large` with `cmd/fixturegen`, which takes a few minutes, makes boards of 200, 2,000, and 5,000 nodes, and writes the addresses of the tablet's runs to `web/app/bench-results/tablet.md`. An instance loads each seed once: give `STORYCREEP_BENCH_SEED` a number that the instance has not had, three apart from the last, for each new load.
3. Sign the tablet's Chrome in to the instance as the checklist's account. Close every other tab, and plug the tablet in.

## 2. Measure

Open each address of `tablet.md` on the tablet, in a new tab, and do not touch the screen while the run goes: it pans, or pans and zooms, by itself, warming up and then recording for its seconds, as its status says, and then shows its results under the canvas. Record them in the release's checklist.

| Target | The run | Passes when |
| --- | --- | --- |
| NFR-PERF-04 | `medium`, `bench=load`, in a tab opened anew, after clearing the site's cached files | The first frame of the board is under 3,000 ms. |
| NFR-PERF-01 | `medium`, `bench=panzoom` | 30 frames a second or more, and the time between frames is under 33 ms at the 95th percentile. |
| FR-EDIT-01 AC3 | `dense`, `bench=panzoom` | As NFR-PERF-01, on the board of 2,000 nodes. |
| FR-EDIT-11 AC1 | The boards of 200 and of 2,000 nodes, `bench=pan` | The page's work in a frame on the board of 2,000 is within 20% of the board of 200's, at the median and at the 95th percentile; the nodes in the canvas are the same. |
| FR-EDIT-11 AC2 | The board of 5,000 nodes, `bench=pan&seconds=300&warm=pass`, which warms up for a pass of 500 of its nodes, after which PixiJS's caches of text, which are bounded, are full | Read the JavaScript heap in remote DevTools (`chrome://inspect`, Memory) once the run's status says Recording…, and at its end, each after collecting the garbage: it grows less than 10%. The GPU textures that the run reports are as many at its end as at its start. |
| NFR-PERF-18 | `medium`, `bench=pan&seconds=300&warm=pass` | As FR-EDIT-11 AC2; and the tab's memory in Chrome's task manager stays under 400 MB. |
| NFR-PERF-06 | `medium`, `bench=edit&seconds=30`, which moves a node in view a step of the grid and back, twice a second, as a drag of it does once it lands | The time from each edit to the frame that shows it is under 50 ms at the 95th percentile. |
| NFR-PERF-05 | `large`, `bench=load`, on the reference desktop | The first frame of the board is under 6,000 ms. |
| NFR-PERF-02 | `medium`, `bench=panzoom`, on the reference desktop | 60 frames a second or more, and the time between frames is under 16 ms at the 95th percentile. |

`renderer=canvas` in a run's address draws with the Canvas 2D renderer, which a device without WebGL uses (ADR-013), and `renderer=webgl` with WebGL, whatever the browser's own choice: the results name the renderer that drew.

## 3. Check by hand

On the tablet, on any board of `medium`:

- Pinch to zoom in and out: the point between the fingers stays under them (FR-EDIT-01 AC1). Two fingers that move together pan, and a third finger changes nothing (NFR-TOUCH-07).
- One finger drags the board in the pan mode; in the select mode, which the toolbar's Select shows pressed, it draws a rectangle that selects the nodes that it overlaps (NFR-TOUCH-03).
- Pulling down from the canvas's top neither reloads the page nor scrolls it (NFR-TOUCH-08).
- Below 40% zoom, nodes are blocks of colour without text (FR-EDIT-01 AC4).
- With Android's Remove animations on, fit to content and zoom to selection jump, rather than move (NFR-A11Y-07).
- Fit to content, zoom to selection, the zoom's buttons, and the minimap each work with a tap, with nothing that needs a long press, a hover, or a second button (FR-EDIT-01 AC2).
- A finger's drag that starts on a selected node moves it, and the selection with it, from 8 px of its press, and a tap that moves less selects it rather than moves it (NFR-TOUCH-05); in the pan mode, a drag that starts on a node that is not selected pans. After a move of two nodes or more, the toolbar offers Undo the move for 10 seconds.
- A selected element has a handle of a fingertip's size on its right edge: a finger's drag of it to another node connects them, and one dropped on the empty board offers to create a node there, which Cancel leaves nothing of (FR-EDIT-03 AC3, FR-EDIT-09). A tap on a connection's line selects it, and a drag of the handle at either of its ends reconnects it. A selected jumper has Go to target above its card, which one tap follows (FR-EDIT-05).
- A second tap on an element opens its text over its card. With the on-screen keyboard open, the editor stays whole above it, and the caret stays in view as you type line after line (FR-EDIT-09 AC3, FR-TEXT-01 AC3), in portrait and in landscape.
