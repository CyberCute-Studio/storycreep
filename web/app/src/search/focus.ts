// The hand-off of the keyboard's focus to the field of a project's search
// (plan note N72): the search's command, run on another page of the
// project, asks for it before it opens the search's page, whose field takes
// it as it mounts, rather than the page's content, which the router gives it
// otherwise, as to a page that a link opened; the project's pages forget it
// once another page was shown in place of the search's.

let asked = false;

/** Asks the search's page that mounts next to give its field the focus. */
export function askSearchFocus(): void {
  asked = true;
}

/** Reports whether the focus was asked for, and forgets that it was. */
export function takeSearchFocus(): boolean {
  const was = asked;
  asked = false;
  return was;
}

/** Forgets that the focus was asked for, as once a page other than the search's was shown in its place. */
export function forgetSearchFocus(): void {
  asked = false;
}
