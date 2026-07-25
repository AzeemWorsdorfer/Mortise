---
description: Archive completed tickets, specs, and ADRs from this session to the Obsidian vault. Moves (not copies) files and cleans up .scratch/. Use when wrapping up a session or when a feature is fully done and merged.
agent: general
---

Archive completed work from this session to the Obsidian vault.

## Steps

1. Scan for completed items:
   - Tickets in `.scratch/` with frontmatter `status: done`
   - Completed or finalized specs in `.scratch/`
   - ADRs in `docs/adr/` that are finalized (no outstanding revisions)

2. For each completed item, **move** it to the Obsidian vault:
   - Tickets → `/Users/azeemworsdorfer/Obsidian/Mortise/01_Archive/Tickets/`
   - PRDs/Specs → `/Users/azeemworsdorfer/Obsidian/Mortise/01_Archive/PRDs/`
   - ADRs → `/Users/azeemworsdorfer/Obsidian/Mortise/01_Archive/ADRs/`

3. Clean up empty directories in `.scratch/` after moving

4. Update the kanban board at `/Users/azeemworsdorfer/Obsidian/Mortise/Master Kanban.md` — move corresponding cards from Done to Archived lane

5. Report a summary: what was archived, where it went, and any items skipped (with reasons)

## Guardrails

- Only archive items that are truly complete: verified, merged, signed off
- If unsure about any item, skip it and flag it in the report
- Preserve original filenames; add a date prefix only if a name collision would occur
- Do not archive items from `.scratch/` that still have open child tickets

$ARGUMENTS
