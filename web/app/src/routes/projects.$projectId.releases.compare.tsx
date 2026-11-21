import { useQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { projectQuery } from "../project/queries";
import { DiffView } from "../releases/DiffView";
import {
  comparedPair,
  diffQuery,
  draft,
  readyReleasesQuery,
} from "../releases/queries";
import { found, searchId } from "../shell/found";
import { SelectField } from "../ui/Field";
import { Alert, Page, textLink } from "../ui/Page";

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
// address leaves out is the release next to the other.
function Compare() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const session = useSuspenseQuery(sessionQuery).data;
  const ready = useSuspenseQuery(readyReleasesQuery(projectId)).data;
  const { from, to } = comparedPair(ready, search.from, search.to);
  const diff = useQuery({
    ...diffQuery(projectId, from ?? "", to, session),
    enabled: from !== undefined,
  });
  const choose = (side: "from" | "to", value: string) =>
    void navigate({
      search: { from, to, [side]: value },
      replace: true,
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
          {diff.isPending && <p>{t("releases.compare.loading")}</p>}
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
