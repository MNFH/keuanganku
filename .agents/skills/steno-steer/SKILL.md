---
name: steno-steer
description: Generate or refresh project steering files (product.md, tech.md, structure.md, plus database.md, api.md, auth.md and others the repo calls for) that steno injects into task packets. Use when the user says "generate steering", "set up project context", or after `steno init` on a project with no steering.
---

# steno-steer — project steering

Goal: the briefing a senior engineer gives a new hire, as `.steno/steering/*.md`. Bar: product.md 1.5–2 KB, tech.md 1.5–2.5 KB, structure.md 2–4 KB, every line a specific fact. Specific beats short; keyword lists and semicolon chains fail `steno steer lint`.

## Steps
1. `steno steer scan`. **greenfield** → stop: nothing to record yet; offer `steno-brainstorm` (brief) or `steno-spec`, never invent a product or stack. Otherwise `steno steer list`; a budget under 4000 → set `steeringBudget: 4000` in `.steno/config.yaml`. Core three always; topic files on evidence; `weak` ones only on request.
2. Tell the user the suggested set in five lines or fewer; ask only what to drop or add (`steno steer new <name> --match "<globs>"` for a custom topic). Skip if told "just generate".
3. `steno steer new --all` writes skeletons for missing files. For a file that already exists, `steno steer template <file>` prints the skeleton: rewrite the file to that shape; do not patch the old text.
4. **Core three first, with most of the effort.** Read README, `docs/`, every manifest and config, the tree two levels deep, the entry points, the schema. Then write:
   - `product.md`: identity sentence (kind of app, where it runs); core problem; `**Feature** — what it does` with limits, formats, types, code lengths, one line per feature; audience and locales; domain model and invariants in plain words; out of scope.
   - `tech.md`: stack by layer with versions from the manifest; a ```bash block of every script with a comment; key config files; conventions; "Do not" with the wrong tools a newcomer reaches for.
   - `structure.md`: a fenced tree with a comment per folder; one section per area with a subfolder → responsibility table; where new code and tests go; the conventions a reviewer cites, naming the helper or component.
   The core three together outweigh any topic file; under 5 KB total means you stopped early.
5. Topic files next, 0.5–1.5 KB each, from the `read:` list plus one or two files in the matched folder. Only what the core three do not say. Drop unused headings. `database.md` is the exception: its `## Tables` section is the output of `steno steer schema` (zero tokens, exact), trimmed of columns nobody queries, with every foreign key kept and a line of words for each relation that needs them; it may run to 3 KB.
6. Every file: one bullet per fact; real names (version, path, helper, env var, number); say which way is current when code disagrees; no code dumps beyond a tree or command block; no `<placeholder>` left.
7. `steno steer lint`. Fix every error and warning (`thin`, `shape`, `packed`, `placeholder`, `no-match`) unless the user disagrees. Show `steno steer list` and stop; do not read the files back.

## Refreshing later
- One area changed: edit that file, then `steno steer lint`. Stack change: `steno steer scan` again.
- Kiro users: `steeringDir: .kiro/steering` in `.steno/config.yaml`; same format.

## Keep context small
- Read what step 4 and the `read:` lists name, not the whole repo. One read-only sub-agent per file when available, returning facts.
- Fetch an existing file with `steno show steering/<file>`; never cat the folder.
