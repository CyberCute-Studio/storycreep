// The images of a project's content (PRD FR-ASSET-02): a component's image
// and an element's cover, which an editor uploads, and the page shows by
// their thumbnails.

import { routes } from "@storycreep/shared/api";

/**
 * The URL of an asset's thumbnail, which the browser reads with the page's
 * session, of the path that the API's description gives it.
 */
export function thumbnailOf(projectId: string, assetId: string): string {
  return routes.getAssetThumbnail.path
    .replace("{project_id}", encodeURIComponent(projectId))
    .replace("{asset_id}", encodeURIComponent(assetId));
}

/** The types of image that the server stores (PRD FR-ASSET-02). */
export const imageTypes =
  "image/png,image/jpeg,image/gif,image/webp,image/svg+xml";
