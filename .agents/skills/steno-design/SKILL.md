---
name: steno-design
description: Write or revise the technical design of a steno spec (design.md) so every requirement is covered by a named, traceable section. Use when the spec phase is "design".
---

# steno-design — design phase

Goal: `design.md` that names the components, data, failure modes and correctness properties needed to satisfy every requirement, readable in five minutes, with traceability precise enough that `steno ctx` can slice one task's sections.
Hard rules: read code, never run it. No test, lint or build runs in this phase; nothing is implemented yet. Never read the spec files back in full (`steno show <id>`).

## Steps
1. `steno status`; confirm phase is `design`, then `steno scaffold` (writes `design.md` if missing; harmless otherwise). `steno show requirements` for the outline; `steno show R<n>` only when designing the section that satisfies it. `steno show glossary` once.
2. Look at the existing code for the areas involved: current components, data fields, patterns to extend. Prefer extending what exists. If a sub-agent is available, one bounded explorer (≤ 15 files, ≤ 250 words) is enough.
3. Fill `design.md` keeping the template headings. Rules:
   - **Readable**: short paragraphs or bullets, never a 60-word sentence. One diagram (mermaid) in Architecture. Interface signatures or props in code blocks of ≤ 10 lines; no implementation bodies.
   - **Ids**: every Components / Data / Error / Property section has `{#D-<slug>}`. Stable; never renumber.
   - **Traceability**: `_Satisfies: 1.1, 1.2_` only on the section that *implements* those criteria, at most 6 per section, and each criterion on one or two sections. Overview, Testing Strategy and Open questions carry no footer.
   - **Files**: name real paths in backticks (`src/…`); `steno ctx` uses them to find the likely files per task.
   - **Correctness Properties**: for pure logic, write `### Property N: <invariant>` with `_Validates: 1.2_` and a *For any …* sentence. These become property-based tests.
   - **Testing Strategy**: a table mapping criteria ranges to test type and file. No commands to run now.
   - Rejected alternatives in one line each. Decisions the user must make go under a final `## Open questions`; leave the heading out when there are none.
4. `steno lint design`. Fix `design/unknown-req`; cover every `design/uncovered-req`; split or retag on `design/over-tagged` and `design/overlap`.
5. Ask for approval with `steno show design` (outline with ids and satisfied criteria) and `steno trace`. Offer a fresh-context review first for large designs (`steno-review`). If `## Open questions` is not empty, offer to interview the user one question at a time (recommended answer first), write each answer into its section and delete the question, and the heading with the last one; they may skip and approve as is.
6. On the user's yes run `steno approve design`, then continue with `steno-tasks`.
