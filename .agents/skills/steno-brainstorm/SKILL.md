---
name: steno-brainstorm
description: Stress-test a rough idea before writing requirements and save the agreed direction as brief.md.
---

# steno-brainstorm — concept brief

Goal: `brief.md` in the spec folder with the problem, the chosen direction, rejected alternatives, open questions and a decision. Short: under 80 lines.

## Steps
1. If the spec does not exist: `steno new <slug> --brief` (writes only `brief.md`). Note the folder it prints.
2. **EGM, only on request.** If the user asked for EGM in this request and it is installed (an `egm-brainstorm` skill or `/egm:egm-brainstorm` command is available): invoke it with the user's idea and this instruction: "Save the concept brief to `<spec folder>/brief.md` using headings Problem, Proposed direction, Alternatives considered, Open questions, Decision." EGM runs fresh-context reviewers against the idea; let it finish, then go to step 5.
3. **Otherwise** (the default), run a lightweight version yourself:
   - Ask the user at most three questions, one at a time: who has the problem, what happens today, what would make them say it is solved.
   - Propose two or three directions in one paragraph each, with the main risk of each.
   - If this session can start a sub-agent with no shared history, give it only the idea and the directions and ask it for the strongest objection to each. Fold the objections in. If it cannot, say so and skip; do not pretend to review.
4. Write `brief.md`. Record the user's choice under `## Decision`, with what is explicitly out of scope.
5. `steno approve brief`, then continue with the `steno-spec` skill, which scaffolds and writes `requirements.md`. The brief is input to requirements, not a substitute.
