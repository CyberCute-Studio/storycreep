// A component whose text all comes from the message catalog, which the lint
// rule in ../hardcoded-text.grit does not report (lint.test.ts in
// web/shared/src/i18n): literals that are keys, attributes that people do not
// read, comparisons, and text without a letter, including HTML entities and
// escape sequences.

export function Translated(props: {
  t: (key: string, args?: Record<string, unknown>) => string;
  count: number;
  status: string;
}) {
  const { t } = props;
  return (
    <section
      title={t("board.settings")}
      className="board-settings"
      id="settings"
      data-testid="settings"
    >
      {t("board.settings")}
      {t("board.count", { count: props.count })}
      {props.status === "draft" ? t("board.draft") : t("board.published")}
      {props.status === "draft" && t("board.unsaved")} <span>·</span>
      <span>&nbsp;&middot;&#160;&#xA0;</span>
      <span>{props.count}</span>
      <span>{`${props.count}`}</span>
      <img alt="" src="board.png" />
      <a href="https://example.com/docs">{t("docs.link")}</a>
      <pre>{"\n"}</pre>
      <span>{"\u00a0"}</span>
      <span>{`\t${props.count}`}</span>
      {/* A comment is not shown. */}
    </section>
  );
}
