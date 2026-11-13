import type { Result, Span } from "@storycreep/shared/api";
import { useInfiniteQuery, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { type FormEvent, Fragment, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { api, pagedQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useCommandHandlers } from "../commands/registry";
import { projectQuery } from "../project/queries";
import { takeSearchFocus } from "../search/focus";
import { searchText } from "../search/query";
import { Button, MoreButton } from "../ui/Button";
import { SelectField, TextField } from "../ui/Field";
import { Alert, Page, Status, textLink } from "../ui/Page";

/** A kind of what the search finds, which its filter narrows the results to. */
type Kind = Result["type"];

const kinds: readonly Kind[] = ["node", "connection", "variable", "component"];

/** The search of the page: what to search for, and of which kind, which a link names. */
interface SearchSearch {
  q?: string;
  type?: Kind;
}

export const Route = createFileRoute("/projects/$projectId/search")({
  // What the address spells, as the router reads it (router.ts): a query
  // of true or false, or of a number as String writes it, as 42, is read as
  // a boolean or a number, whose text is the query's all the same; and a
  // query as the server takes it, of at most 200 characters, without
  // control characters (searchText).
  validateSearch: (search: Record<string, unknown>): SearchSearch => {
    const out: SearchSearch = {};
    const q =
      typeof search.q === "string" ||
      typeof search.q === "number" ||
      typeof search.q === "boolean"
        ? searchText(String(search.q))
        : "";
    if (q !== "") {
      out.q = q;
    }
    if (kinds.includes(search.type as Kind)) {
      out.type = search.type as Kind;
    }
    return out;
  },
  component: SearchPage,
});

/** The results of a search of a project, a page at a time, in the order of how well they match (PRD FR-PROJ-03). */
function searchQuery(projectId: string, q: string, type: Kind | undefined) {
  return pagedQuery(
    ["projects", projectId, "search", q, type ?? null],
    async (query, signal) =>
      (
        await api.call(
          "searchProject",
          {
            path: { project_id: projectId },
            query: { ...query, q, ...(type === undefined ? {} : { type }) },
          },
          { signal },
        )
      ).body,
    { limit: 50, meta: { showsRefusals: true } },
  );
}

// A project's search (PRD FR-PROJ-03, plan note N72): a field of what to
// search for and a filter of the kind of what it finds, which the page's
// address holds, so that Back goes to the search before and a link names
// one, and the results, a page at a time, each with its kind, its board,
// and the words of its title and its text that matched marked (AC2), and
// a link to where it is: a node, or a connection, on its board's canvas,
// by its node, which the canvas selects and shows whole, as a usage list's
// site does (plan item 61), and a variable or a component in its panel.
// The search's command, Ctrl+/ on every platform, gives the field the focus here,
// and opens the page with the focus in it from the project's other pages
// (FR-EDIT-10).
function SearchPage() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const { q, type } = Route.useSearch();
  const navigate = Route.useNavigate();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const field = useRef<HTMLInputElement>(null);
  // Whether the field takes the focus once the address has the query that
  // the palette's search asked for here.
  const focusAsked = useRef(false);
  // The field shows the address's query once it changes otherwise than by
  // the field, as by Back, or by the palette's search, in place of what was
  // typed in it and not sent; it is the same field, which keeps the focus
  // that it has, as once Enter sent it (WCAG 2.4.3).
  useEffect(() => {
    const input = field.current;
    if (input !== null && input.value.trim() !== (q ?? "")) {
      input.value = q ?? "";
    }
    if (takeSearchFocus() || focusAsked.current) {
      focusAsked.current = false;
      input?.focus();
      input?.select();
    }
  }, [q]);
  useCommandHandlers("page", {
    "project.search": {
      run: (asked) => {
        const query = asked === undefined ? undefined : searchText(asked);
        if (query !== undefined && query !== "" && query !== q) {
          focusAsked.current = true;
          void navigate({ search: (prev) => ({ ...prev, q: query }) });
          return;
        }
        // The search of the query that the page shows already: the field
        // shows it too, in place of what was typed in it and not sent.
        const input = field.current;
        if (input !== null && query !== undefined && query !== "") {
          input.value = query;
        }
        input?.focus();
        input?.select();
      },
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const text = searchText(
      String(new FormData(event.currentTarget).get("q") ?? ""),
    );
    void navigate({
      search: (prev) => ({ ...prev, q: text === "" ? undefined : text }),
    });
  };
  return (
    <Page
      title={t("search.title")}
      documentTitle={t("search.page_title", { project: project.name })}
      width="wide"
    >
      <search>
        <form
          onSubmit={submit}
          className="flex flex-wrap items-end gap-2"
          noValidate
        >
          <div className="min-w-48 flex-1">
            <TextField
              ref={field}
              label={t("search.field")}
              hint={t("search.hint")}
              name="q"
              type="search"
              autoComplete="off"
              maxLength={200}
              defaultValue={q ?? ""}
            />
          </div>
          <SelectField
            label={t("search.kind_filter")}
            value={type ?? ""}
            onChange={(e) => {
              const value = e.currentTarget.value;
              void navigate({
                search: (prev) => ({
                  ...prev,
                  type: value === "" ? undefined : (value as Kind),
                }),
              });
            }}
          >
            <option value="">{t("search.kind_any")}</option>
            {kinds.map((kind) => (
              <option key={kind} value={kind}>
                {t("search.kinds", { kind })}
              </option>
            ))}
          </SelectField>
          <Button type="submit" variant="primary">
            {t("search.submit")}
          </Button>
        </form>
      </search>
      {q === undefined ? (
        <p className="text-fg-muted">{t("search.intro")}</p>
      ) : (
        <Results projectId={projectId} q={q} type={type} />
      )}
    </Page>
  );
}

/** The results of a search for q, of type, if it says one. */
function Results({
  projectId,
  q,
  type,
}: {
  projectId: string;
  q: string;
  type: Kind | undefined;
}) {
  const { t } = useTranslation();
  const list = useInfiniteQuery(searchQuery(projectId, q, type));
  const items = list.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <section aria-label={t("search.results")} className="space-y-4">
      <Status
        message={
          list.isPending
            ? t("search.searching")
            : list.isError
              ? undefined
              : items.length === 0
                ? t("search.none", { query: q })
                : t("search.shown", {
                    count: items.length,
                    more: list.hasNextPage ? "yes" : "no",
                  })
        }
      />
      <Alert
        message={list.isError ? refusalOf(list.error, t)?.message : undefined}
      />
      {items.length > 0 && (
        <ol className="space-y-4">
          {items.map((result) => (
            <ResultItem
              key={`${result.type}:${result.id}`}
              projectId={projectId}
              result={result}
            />
          ))}
        </ol>
      )}
      <MoreButton list={list}>{t("search.more")}</MoreButton>
    </section>
  );
}

/** Text in parts, of which those that matched a word of the query are marked. */
function Marked({ parts }: { parts: readonly Span[] }) {
  return parts.map((part, i) =>
    part.match ? (
      // biome-ignore lint/suspicious/noArrayIndexKey: the parts are the text's, in its order, which a result never reorders.
      <mark key={i} className="rounded-sm bg-surface-hover px-0.5 text-inherit">
        {part.text}
      </mark>
    ) : (
      // biome-ignore lint/suspicious/noArrayIndexKey: as above.
      <Fragment key={i}>{part.text}</Fragment>
    ),
  );
}

/**
 * A result: its title, marked, which links to where it is, its kind, its
 * board, or that it is global, and the snippet of its text, marked.
 */
function ResultItem({
  projectId,
  result,
}: {
  projectId: string;
  result: Result;
}) {
  const { t } = useTranslation();
  const untitled = t("boards.untitled_node");
  const node =
    result.node_title === undefined || result.node_title === ""
      ? untitled
      : result.node_title;
  const title =
    result.type === "connection" ? (
      t("search.connection_from", { node })
    ) : result.title.length === 0 ? (
      untitled
    ) : (
      <Marked parts={result.title} />
    );
  const where =
    result.board_name !== undefined
      ? t("search.on_board", { board: result.board_name })
      : result.type === "variable"
        ? t("search.global")
        : undefined;
  return (
    <li className="space-y-1 break-words" data-result={result.id}>
      <p className="font-medium">
        {(result.type === "node" || result.type === "connection") &&
        result.board_id !== undefined &&
        result.node_id !== undefined ? (
          <Link
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: result.board_id }}
            search={{ node: result.node_id }}
            className={textLink}
          >
            {title}
          </Link>
        ) : result.type === "variable" ? (
          <Link
            to="/projects/$projectId/variables"
            params={{ projectId }}
            search={{ variable: result.id }}
            className={textLink}
          >
            {title}
          </Link>
        ) : result.type === "component" ? (
          <Link
            to="/projects/$projectId/components"
            params={{ projectId }}
            search={{ component: result.id }}
            className={textLink}
          >
            {title}
          </Link>
        ) : (
          title
        )}
      </p>
      <p className="text-sm text-fg-muted">
        {t("search.kinds", { kind: result.type })}
        {where !== undefined && ` · ${where}`}
      </p>
      {result.snippet.length > 0 && (
        <p className="whitespace-pre-line">
          <Marked parts={result.snippet} />
        </p>
      )}
    </li>
  );
}
