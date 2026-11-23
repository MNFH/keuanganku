---
name: steno-tasks
description: Break an approved steno design into an ordered, traceable task list (tasks.md). Use when the spec phase is "tasks".
---

# steno-tasks — task breakdown

Goal: `tasks.md` where every leaf task is one sitting of work, references the criteria it satisfies, and the list is ordered so each task leaves the code working.

## Steps
1. Run `steno status`; confirm phase is `tasks`, then `steno scaffold` (writes `tasks.md` if missing; harmless otherwise). Use `steno show design` and `steno show requirements` (outlines) to see the ids.
2. Write `tasks.md`. Rules:
   - Groups: `- [ ] 1. <group>`. Leaves: `  - [ ] 1.1 <step>`. Optional leaves: `- [ ]* 1.2 <step>` (extra tests, polish).
   - Each leaf has a footer line `    - _Requirements: 1.1, 1.2_ _Design: D-auth, D-prop-1_`. Use criterion ids when a task covers only part of a requirement. List the design section the task implements first; `steno ctx` protects it from trimming.
   - Order by dependency, data model before logic before UI. Tests belong in the task that adds the behaviour, not in a final "write tests" task.
   - One leaf = one commit-sized change that can be verified. If you cannot name how to verify it, split it or drop it.
   - After each surface (a page, a module, an API) add a top-level `- [ ] N. Checkpoint — <surface> end to end` with its Verify line: type check, test command, and the user-visible flows to try. No footer; `steno ctx` hands it the criteria of every task since the previous checkpoint. End the list with a final checkpoint.
   - No tasks for things the design does not mention. If the design is missing something, say so instead of inventing a task.
3. Run `steno lint tasks`. Fix unknown ids. Every requirement should be covered (`tasks/uncovered-req`).
4. Check `steno trace --criteria`: no criterion on zero tasks, few on more than two. Show `steno show tasks` and `steno trace` to the user and ask for approval.
5. On approval run `steno approve tasks`. Phase becomes `execute`; hand over to the `steno-run` skill.
