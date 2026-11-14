// The reads of a project that the server derives of the whole of its
// content, which nearly any operation changes: its writing statistics (PRD
// FR-TEXT-06, plan note N76), its validation report (FR-DEP-03), and its
// dependency graph (FR-DEP-02), with the contributions to its edges. The
// project's document has each read again as an operation applies (Follower),
// the page's own or another session's, so that what a page shows of them
// follows an edit.

import type { QueryClient, QueryKey } from "@tanstack/react-query";

/**
 * Has the reads of queryKey that a page shows read again as operations
 * apply, at most once a pause, as the project's document asks it to
 * (follow).
 */
export class Follower {
  /** The wait after the reads were read again, while it lasts. */
  private timer: ReturnType<typeof setTimeout> | undefined;
  /** Whether an operation applied during the wait, which its end has the reads read again for. */
  private again = false;
  /** Whether the follower waits for the answers of reads that were under way as an operation applied, which have them read again. */
  private awaited = false;
  private disposed = false;

  constructor(
    private readonly client: QueryClient,
    private readonly queryKey: QueryKey,
    /** How long, in milliseconds, the follower waits after it has had the reads read again before it has them read again for the operations that applied meanwhile. */
    private readonly pause: number,
  ) {}

  /**
   * Has the reads that a page shows read again, as an operation applies: at
   * once, unless they were read again less than pause ago, and then once
   * more at the end of that wait, for the operations that applied during
   * it, so that they follow each edit within the pause, however many
   * operations apply. A read that is under way is let finish rather than
   * begun anew (cancelRefetch), since one begun anew as each operation
   * applies, which waits again for the page's batches (whenSent) and for
   * the server, would never be answered while the operations come faster
   * than that, as on a slow connection; and as it may have read the server
   * before the operation applied, they are read again once it is answered,
   * as they are once one that a page's loader reads is, which no page shows
   * yet, and whose answer would otherwise be taken for one of after the
   * operation. The follower waits for the answers of the reads under way,
   * rather than look again at the end of each wait while they are, so that
   * one that waits for long, as a node's statistics that wait for the
   * page's batches while the page is offline (nodeStatisticsQuery), keeps
   * no timer going meanwhile. Those that no page shows are marked alone, so
   * that a page that shows them later reads them again, and a document of
   * whose reads no page shows any, and reads none, keeps no timer, which
   * would hold it once it is dropped.
   */
  follow(): void {
    if (this.disposed) {
      return;
    }
    if (this.timer !== undefined) {
      this.again = true;
      return;
    }
    const { client, queryKey } = this;
    const cache = client.getQueryCache();
    const reading = cache
      .findAll({ queryKey, fetchStatus: "fetching" })
      .flatMap((query) => query.promise ?? []);
    void client.invalidateQueries({ queryKey }, { cancelRefetch: false });
    if (reading.length > 0 && !this.awaited) {
      this.awaited = true;
      void Promise.allSettled(reading).then(() => {
        this.awaited = false;
        this.follow();
      });
    }
    if (cache.findAll({ queryKey, type: "active" }).length === 0) {
      return;
    }
    this.timer = setTimeout(() => {
      this.timer = undefined;
      if (this.again) {
        this.again = false;
        this.follow();
      }
    }, this.pause);
  }

  /** Stops following, as the project's document is dropped. */
  dispose(): void {
    this.disposed = true;
    clearTimeout(this.timer);
  }
}
