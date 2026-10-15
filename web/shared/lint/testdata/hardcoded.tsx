// A component that shows text that is not from the message catalog in nine
// places, each of which the lint rule in ../hardcoded-text.grit reports
// (lint.test.ts in web/shared/src/i18n).

export function Hardcoded(props: {
  count: number;
  open: boolean;
  name: string;
}) {
  return (
    <section title="Board settings">
      Board settings
      {"Save"}
      {`${props.count} boards`}
      {props.open ? "Open" : "Closed"}
      {props.open && "Unsaved changes"}
      {/* biome-ignore lint/style/useTemplate: the rule is tested with + */}
      {"Hello, " + props.name}
      <img alt="The board" src="board.png" />
      <input aria-label={`Search ${props.name}`} />
    </section>
  );
}
