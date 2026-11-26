import { compile, Project } from "@storycreep/shared/expr/compile";
import { expect, test } from "vitest";
import { compareCodePoints, Prover } from "./overlap";

// The project of the cases, of the int trust, the float mood, the string
// name, and the bools met and left, and a node, as the server's own cases
// are (overlap_test.go, TestExclusive).
const node = "nd_00000000000000000000000001";
const project = Project.of(
  [
    { id: "var_00000000000000000000000002", key: "trust", type: "int" },
    { id: "var_00000000000000000000000003", key: "mood", type: "float" },
    { id: "var_00000000000000000000000004", key: "name", type: "string" },
    { id: "var_00000000000000000000000005", key: "met", type: "bool" },
    { id: "var_00000000000000000000000006", key: "left", type: "bool" },
  ],
  [],
  [{ id: node }],
);

const condition = (source: string) =>
  source === ""
    ? undefined
    : compile(source.replaceAll("NODE", node), "condition", project, "");

test.each<[string, string, boolean]>([
  // None always holds, and false never does.
  ["", "", false],
  ["", "met", false],
  ["", "false", true],
  ["false", "met", true],
  // A condition and its negation, either way round, however written.
  ["met", "not met", true],
  ["!met", "met", true],
  ['visited(node("NODE"))', 'not visited(node("NODE"))', true],
  ["met", "not left", false],
  ["met", "met", false],
  // Complementary comparisons of the same operands.
  ["trust < mood", "trust >= mood", true],
  ["trust < mood", "mood <= trust", true],
  ["trust < mood", "mood < trust", false],
  ["trust == mood", "trust != mood", true],
  // Literals that no value meets both comparisons with.
  ["trust == 1", "trust == 2", true],
  ["trust == 1", "1 == trust", false],
  ["trust == 1", "trust == 1.0", false],
  ["trust == 1", "trust != 1", true],
  ["trust != 1", "trust != 2", false],
  ["trust < 3", "trust >= 3", true],
  ["trust < 3", "3 <= trust", true],
  ["trust > 5", "trust < 2", true],
  ["trust > 2", "trust < 3", false],
  ["trust >= 3", "trust <= 3", false],
  ["trust > 3", "trust <= 3", true],
  ["mood > 0.5", "mood < 0.5", true],
  // A negative number, the unary - of a literal, as the literal of its
  // value, but no other negation.
  ["trust == -1", "trust == -2", true],
  ["trust == -1", "trust == 1", true],
  ["trust < -5", "trust > 0", true],
  ["-5 > trust", "trust >= 0", true],
  ["mood <= -0.5", "mood > 0.5", true],
  ["trust > -5", "trust < -2", false],
  ["trust == -0", "trust == 0", false],
  ["-trust == 1", "-trust == 2", true],
  ["-trust == 1", "trust == 1", false],
  // Strings in the order of their code points, the empty one first.
  ['name == "Elara"', 'name == "Mira"', true],
  ['name == "Elara"', 'name != "Elara"', true],
  ['name < "b"', 'name > "c"', true],
  ['name == "a"', 'name > "a"', true],
  ['name >= "b"', '"a" >= name', true],
  ['name < "b"', 'name > "a"', false],
  ['name < "é"', 'name > "z"', false],
  ['name <= ""', 'name != ""', true],
  ['name != "a"', 'name != "b"', false],
  // Code points above the basic plane come after every one of it, as
  // UTF-8 orders them, where UTF-16 would put them before U+E000.
  ['name < "😀"', 'name > ""', false],
  ['name > "😀"', 'name < ""', true],
  // Booleans, of which there are two.
  ["met == true", "met == false", true],
  ["met != true", "met != false", true],
  ["met == true", "met != false", false],
  ["trust == 1", "mood == 2", false],
  // A conjunction one of whose terms is exclusive with the other, and a
  // disjunction both of whose terms are.
  ["met and trust > 3", "trust < 2", true],
  ["met and trust > 3", "left and trust < 2", true],
  ["met and trust > 3", "left and trust > 2", false],
  ["trust < 2 or trust > 8", "trust >= 3 and trust <= 7", true],
  ["trust < 2 or trust > 8", "trust == 5 or trust == 9", false],
  ["(met and left)", "not (met and left)", true],
  // A disjunction is the negation of the other condition, or of a term of
  // its conjunction, though neither of its terms is exclusive with it
  // alone.
  ["met or left", "not (met or left)", true],
  ["met or left", "trust > 3 and not (met or left)", true],
  ["met or left", "trust < 2 or not (met or left)", false],
])("%s and %s are exclusive: %s", (a, b, exclusive) => {
  expect(new Prover().exclusive(condition(a), condition(b))).toBe(exclusive);
  expect(new Prover().exclusive(condition(b), condition(a))).toBe(exclusive);
});

// Verifies: FR-DEP-03/AC1
test("a proof that needs more than the prover's steps proves nothing", () => {
  // Two disjunctions of 120 terms each, every term of one exclusive with
  // every term of the other, are exclusive, but proving it compares 14,400
  // pairs of terms. A prover's proofs share its steps, but a condition
  // that is false, which needs no step, is exclusive with any.
  const balanced = (terms: string[]): string =>
    terms.length === 1
      ? terms[0]
      : `(${balanced(terms.slice(0, terms.length / 2))}) or (${balanced(terms.slice(terms.length / 2))})`;
  const disjunction = (from: number, n: number) =>
    condition(
      balanced(Array.from({ length: n }, (_, i) => `trust == ${from + i}`)),
    );
  const one = disjunction(0, 1);
  const many = disjunction(0, 120);
  const b = disjunction(1000, 120);
  const spent = new Prover();
  expect(spent.exclusive(many, b)).toBe(false);
  expect(spent.exclusive(one, b)).toBe(false);
  const falsity = condition("false");
  expect(spent.exclusive(falsity, b)).toBe(true);
  expect(spent.exclusive(b, falsity)).toBe(true);
  expect(new Prover().exclusive(one, b)).toBe(true);
});

test("strings compare by their code points", () => {
  expect(compareCodePoints("", "a")).toBe(-1);
  expect(compareCodePoints("a", "a")).toBe(0);
  expect(compareCodePoints("ab", "a")).toBe(1);
  expect(compareCodePoints("😀", "")).toBe(1);
  expect(compareCodePoints("", "😀")).toBe(-1);
});
