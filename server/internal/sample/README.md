# The Lighthouse Keeper — the sample project

The sample project of PRD FR-PROJ-05 (plan item 85, note N92). A user opens it in one click on a workspace's page,
which `POST /api/v1/workspaces/{workspace_id}/sample-project` imports as a new project with fresh IDs. The docs, the
demo, and the end-to-end tests use it too.

[`the-lighthouse-keeper.zip`](the-lighthouse-keeper.zip) is an authoring export of the project's draft
(`schema/authoring/v1`), content-only and without assets, and it is the sample's source: there is no other.
The server embeds it (package `sample`).

## The story

The lighthouse on the point has been dark for three nights. The player arrives by ferry, hears from Mira that her
sister Elara, the keeper, will not come down from the tower, and takes on the quest to bring clean lamp oil up from
the harbourmaster's storehouse. Tomas, the harbourmaster, sells it only once he trusts the keeper's word, or the
player's.

| Board | Nodes | What it shows |
| --- | --- | --- |
| Harbour Intro | 13 | The project's entry. Mira's choices, a value shown in a text (`coins`), a choice gated on a condition, a branch on the quest's stage, and the story's ending. Its jumpers lead to the other two boards. |
| Elara — First Meeting | 15 | Elara's speeches, a board's own variable that entering the board resets (`questions_asked`), scripts that write variables and a component's attributes (`elara.trust`, `elara.mood`), the quest's start and end, and a signal (`lighthouse_lit`). |
| Harbourmaster | 12 | Choices gated on what happened on Elara's board: a variable (`met_elara`), an attribute (`elara.trust`), and a visit (`visited(node(…))`), and the purchase that sets `has_oil`. |

- **Variables (6):** `met_elara`, `lamp_quest` (the quest: 0 not started, 1 accepted, 2 the oil is bought, 3 the lamp
  is lit), `has_oil`, `coins`, `questions_asked` (Elara's board's own), and `heard_rumour`. `lamp_quest` and `has_oil`
  are in the folder Quest.
- **Components (4):** Elara, Mira, and Tomas, of the type Character, which the sample gives the attributes Trust,
  Mood, Title, and Biography, in the folder Characters; and the Brass Key, an Item. The project has the five default
  component types that every new project has.
- **Gating across boards:** the dependency view draws edges between every two boards, in both directions, and edges of
  all four kinds: variables, attributes, jumpers, and the visit of Elara at the lamp, which gates a choice of the
  harbourmaster's. [`dependencies.json`](dependencies.json) is the graph that the view draws (FR-DEP-02 AC1).
- **The deliberate warning:** `heard_rumour` is set when Tomas tells the player about the wreck and read nowhere, so the
  validation reports `var_written_never_read` for it, the one finding of the project, which shows what the validation
  panel does (FR-PROJ-05 AC1). The variable's description says so.

## Changing it

1. Open the sample in a workspace of a development instance, and change it in the app.
2. Export its draft from its releases' page: **content only**, without assets, as the archive is now.
3. Replace `the-lighthouse-keeper.zip` with the export, and update this README and `dependencies.json` to match.
4. Run the sample's tests, `go test ./internal/httpserver -run Sample` in `server/`, which need Docker: the import has
   no error and the one warning, the view draws `dependencies.json`, and an export of an import of the archive is the
   archive (FR-PROJ-05 AC2).

The archive keeps the IDs of the project that it was exported from, which an import never takes, as it gives every
entity a fresh ID. Nothing refers to them.
