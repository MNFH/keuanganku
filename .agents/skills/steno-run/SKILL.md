---
name: steno-run
description: Implement a steno spec one task at a time using a budgeted context packet. Use when the spec phase is "execute" or the user says "do the next task" / "continue the spec".
---

# steno-run — execute tasks

Goal: implement exactly one task, verify it, mark it done, stop. Never pull the whole spec into context.

## Loop (per task)
1. `steno next` → task id. If the user named a task, use that id instead.
2. `steno ctx <id>` → the packet: task, its acceptance criteria, its design sections, likely files, matching steering. This is your whole brief. If a pointer like `…(steno show D-auth)` appears and you need it, fetch only that.
3. Read the likely files. Implement the task so its criteria hold. Add or update tests in the same change.
4. Verify: run the project's tests or the check the design names. Do not mark done on red.
5. `steno done <id>`. It prints the next task. Stop and report in two or three lines: what changed, how it was verified, anything that drifted from the spec.
6. Continue to the next task only if the user asked to run through several.

## Rules
- If a task is impossible or wrong as written, do not improvise. Say which criterion or design section conflicts and propose the spec edit. Spec first, then code.
- Keep spec ids in commit messages (`T2.1 add session store (R1.1)`), so `steno review --stage drift` can trace later.
- Optional (`*`) tasks only on request: `steno next --optional`.
