import { useEffect } from "react";
import { PRODUCT_NAME } from "../product";

/**
 * Names the page in the browser's title bar and tab, after the product, as
 * screen readers announce it on each page (WCAG 2.2 success criterion
 * 2.4.2): the product's name alone if page is not given.
 */
export function useDocumentTitle(page?: string): void {
  useEffect(() => {
    document.title =
      page === undefined ? PRODUCT_NAME : `${page} – ${PRODUCT_NAME}`;
  }, [page]);
}
