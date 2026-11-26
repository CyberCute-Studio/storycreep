import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useProjectDocument } from "../document/react";
import { online } from "../offline/online";
import { projectQuery } from "../project/queries";
import { DiffView } from "../releases/DiffView";
import {
  comparedPair,
  diffQuery,
  draft,
  readyReleasesQuery,
} from "../releases/queries";
import { found, searchId } from "../shell/found";
import { Button } from "../ui/Button";
import { SelectField } from "../ui/Field";
import { Alert, Page, Status, textLink } from "../ui/Page";

/** The search of the page: the release that it compares from, and the release, or the draft, that it compares into. */
interface CompareSearch {
  from?: string;
  to?: string;
}

export const Route = createFileRoute("/projects/$projectId/releases/compare")({
  validateSearch: (search: Record<string, unknown>): CompareSearch => {
    const from = searchId("snapshot", search.from);
    const to = search.to === draft ? draft : searchId("snapshot", search.to);
    return {
      ...(from === undefined ? {} : { from }),
      ...(to === undefined ? {} : { to }),
    };
  },
  loader: async ({ context: { queryClient }, params }) => {
    // Offline, the project's pages say that this page needs a connection, in
    // its place, rather than wait for its reads (offline/OfflinePages.tsx).
    if (!online()) {
      return;
    }
    await found(queryClient.ensureQueryData(projectQuery(params.projectId)));
    await found(
      queryClient.ensureQueryData(readyReleasesQuery(params.projectId)),
    );
  },
  component: Compare,
});

// The web diff of two of a project's releases, or of one and the draft (PRD
// FR-SNAP-04, J4, plan note N88), which the address names, so that a link
// reaches it, and which it picks otherwise (comparedPair): a side that the
// address leaves out is the release next to the other. A diff into the draft
// is of the draft as the page read it, which its button reads again, as it
// does a comparison of two releases that failed.
function Compare() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const session = useSuspenseQuery(sessionQuery).data;
  const ready = useSuspenseQuery(readyReleasesQuery(projectId)).data;
  const doc = useProjectDocument(projectId);
  const { from, to } = comparedPair(ready, search.from, search.to);
  // The address takes a side that it left out once it is picked, so that a
  // release that becomes ready later neither moves what the page shows nor
  // spends another diff.
  useEffect(() => {
    if (from !== undefined && (search.from !== from || search.to !== to)) {
      void navigate({
        search: { from, to },
        replace: true,
        resetScroll: false,
      });
    }
  }, [from, to, search.from, search.to, navigate]);
  const diff = useQuery({
    ...diffQuery(doc, from ?? "", to, session),
    enabled: from !== undefined,
  });
  // Still so while it is read again, so that the button keeps the focus.
  const failed = diff.data === undefined && diff.errorUpdateCount > 0;
  const choose = (side: "from" | "to", value: string) =>
    void navigate({
      search: { from, to, [side]: value },
      replace: true,
      resetScroll: false,
    });
  const options = ready.map((release) => (
    <option key={release.id} value={release.id}>
      {release.version}
    </option>
  ));
  // A side that the address names and no ready release is, as one since
  // deleted, is shown as what it is, rather than as the select's first.
  const unlisted = (id: string) =>
    id === draft || ready.some((release) => release.id === id) ? null : (
      <option value={id}>{t("releases.compare.unlisted")}</option>
    );
  return (
    <Page
      title={t("releases.compare.title")}
      documentTitle={t("releases.compare.page_title", {
        project: project.name,
      })}
      width="wide"
    >
      <p>
        <Link
          to="/projects/$projectId/releases"
          params={{ projectId }}
          className={textLink}
        >
          {t("releases.view.back")}
        </Link>
      </p>
      {from === undefined ? (
        <p>{t("releases.compare.none")}</p>
      ) : (
        <>
          <div className="grid gap-4 sm:grid-cols-2">
            <SelectField
              label={t("releases.compare.from")}
              value={from}
              onChange={(event) => choose("from", event.target.value)}
            >
              {unlisted(from)}
              {options}
            </SelectField>
            <SelectField
              label={t("releases.compare.to")}
              value={to}
              onChange={(event) => choose("to", event.target.value)}
            >
              {unlisted(to)}
              {options}
              <option value={draft}>{t("releases.compare.draft")}</option>
            </SelectField>
          </div>
          {(to === draft || failed) && (
            <div className="flex flex-wrap items-center gap-2">
              <Button
                onClick={() => void diff.refetch({ cancelRefetch: false })}
              >
                {t("releases.compare.again")}
              </Button>
              {to === draft && (
                <p className="text-fg-muted">
                  {t("releases.compare.draft_as_read")}
                </p>
              )}
            </div>
          )}
          {diff.isPending && <p>{t("releases.compare.loading")}</p>}
          <Status
            message={
              diff.isRefetching ? t("releases.compare.loading") : undefined
            }
          />
          <Alert
            message={
              diff.isError ? refusalOf(diff.error, t).message : undefined
            }
          />
          {diff.data !== undefined && <DiffView diff={diff.data} />}
        </>
      )}
    </Page>
  );
}
