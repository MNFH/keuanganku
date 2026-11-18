---
name: steno-spec
description: Create a steno spec and write its requirements (requirements.md, EARS acceptance criteria): intake, codebase exploration, outline, detailing, lint, review. Use when the user wants to start a feature or bugfix, "write the requirements", or the spec phase is "requirements".
---

# steno-spec — requirements phase

Goal: `requirements.md` a reviewer with no context can test against: WHAT must be true, never HOW.
Hard rules: never read a spec file back in full (use `steno show <id>`); no code in criteria.

## 0. Resume (the spec already exists)
Run `steno status <slug>` first and branch on it; never redo intake for an existing spec.
- `requirements.md: not started` or `skeleton: requirements.md …` → go to step 2.
- phase `requirements`, no skeleton line, not approved → `steno show requirements`, then go to step 5.
- `requirements` already approved → stop; the next skill is `steno-design`.
- phase `brief` → stop; finish `steno-brainstorm` (`steno approve brief`) first.

## 1. Intake (new spec only; skip what the user already said)
Ask once, in one message: (a) feature or bugfix? (b) mode: **requirements-first** (default, gated), **quick** (all three artifacts, no gates, small work), or **brief first** (unclear idea → `steno-brainstorm`).
Then `steno new <kebab-slug> --title "<Title>"` (add `--bugfix`), or `steno quick <slug>`.

## 2. Explore (one bounded pass)
- If the spec has a brief: `steno show brief`. It is the primary input: its Decision sets scope, its Open questions carry into step 5.
- Steering: read `product.md` and the topic file for the area (`steno show steering/<f>`). If `steno status` printed `steering: none`, offer `steno-steer` first; if it printed nothing about steering the repo is greenfield, so skip steering and rely on the brief and the user.
- Find what the feature touches: pages, queries, data fields, i18n, existing filters or modes.
  If a sub-agent is available, delegate to **one** capped at 15 files: paths, a ≤250-word summary of data fields, modes, conventions, small UI rules worth keeping (markers, ordering, hide/show, fallbacks), and ambiguities. Otherwise look yourself, opening only what you need. Greenfield: skip this; the brief is the code knowledge.
- That summary is all the code knowledge needed; never spawn writers that re-read code.

## 3. Outline
`steno scaffold` writes the `requirements.md` skeleton if it is missing. Write the outline into that file, replacing the `<…>` placeholders under its headings; never a separate file.
- **Introduction**: two short paragraphs: situation today, what changes.
- **Glossary**: `- **Term_Name**: definition` for each view, control, mode and entity the criteria name (`THE Wallet_Picker SHALL …`, never "the dropdown"), defined by what the user sees.
- One `### Requirement N: <title>` per user-visible capability (3–8), each with its `**User Story:**`. Always consider one for **data availability** (what existing records must provide) and one for **modes and localisation**.
In gated mode show titles and stories (not the file) and ask "proceed to detail these?". In quick mode or if told to run through, continue.

## 4. Detail each requirement (you, in this context)
Fill `#### Acceptance Criteria` under each requirement in `requirements.md`: 3–6 each (aim near 4), one EARS sentence, exactly one SHALL, at most 35 words:
`WHEN <trigger>, THE <Term> SHALL …` · `IF <condition>, THEN THE <Term> SHALL …` · `WHILE <state>, …` · `WHERE <mode/option>, …` · `THE <Term> SHALL …`
Cover happy path, empty/zero cases, the inverse action, mode variants, what must NOT happen. Concrete values only when given by the user or data.
Forbidden in criteria: file paths, component/prop/state names, function signatures, CSS, i18n keys. Wanting them means design; note it for `steno-design`.

## 5. Check and hand over
1. `steno lint requirements`; fix every error and warning (`spec/placeholder`: a `<…>` is still unreplaced; `ears/implementation`, `ears/too-long`: rewrite in user terms; `ears/undefined-term`: add the term).
2. Put every ambiguity the explorer flagged, every assumption and invented value under a final `## Open questions` list; never silently pick a side.
3. Report: `steno show requirements`, the open questions, then offer **resolve** (when questions are open: interview the user one question at a time with a recommended answer, write each answer into the requirement, delete the question and the heading with the last one, re-lint), **approve** (`steno approve requirements`, then `steno-design`; open questions carry over), **review** (`steno-review --stage requirements`) or **edit** (one requirement, re-lint).
Never `steno approve` without the user's yes, except in quick mode.
