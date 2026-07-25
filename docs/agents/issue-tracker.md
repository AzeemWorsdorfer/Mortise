# Issue tracker: Project + Obsidian Vault

Issues and specs live as markdown files in `.scratch/` within this repo. The Obsidian vault at `/Users/azeemworsdorfer/Obsidian/Mortise` has a symlink to `.scratch/` so the kanban board (`Master Kanban.md`) can display active tickets.

## Conventions

- One feature per directory: `.scratch/<feature-slug>/`
- The spec is `.scratch/<feature-slug>/spec.md`
- Implementation issues are one file per ticket at `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, numbered from `01`
- Triage state is recorded as a `status` property in YAML frontmatter (see `triage-labels.md` for the role strings)
- Comments and conversation history append to the bottom of the file under a `## Comments` heading
- The kanban board at `/Users/azeemworsdorfer/Obsidian/Mortise/Master Kanban.md` has lanes grouped by the `status` frontmatter property

## Archive workflow

When a ticket, spec, or ADR is completed and reviewed:
1. Move the file from the project repo to the corresponding folder in the Obsidian vault:
   - Tickets → `/Users/azeemworsdorfer/Obsidian/Mortise/01_Archive/Tickets/`
   - Specs → `/Users/azeemworsdorfer/Obsidian/Mortise/01_Archive/PRDs/`
   - ADRs → `/Users/azeemworsdorfer/Obsidian/Mortise/01_Archive/ADRs/`
2. Remove from the project repo so only active work remains
3. Move the kanban card to the Archived lane before removal

## When a skill says "publish to the issue tracker"

Create a new file under `.scratch/<feature-slug>/issues/` (creating the directory if needed) with frontmatter:

```yaml
---
status: needs-triage
---
```

Then append a card to the Needs Triage lane in the kanban board:

```markdown
- [ ] [[.scratch/<feature-slug>/issues/<NN>-<slug>]]
```

## When a skill says "fetch the relevant ticket"

Read the file at the referenced path. The user will normally pass the path or the issue number directly.

## Reading the kanban board

Parse `/Users/azeemworsdorfer/Obsidian/Mortise/Master Kanban.md` to find tickets by lane. The lanes are:

- **Needs Triage** — `status: needs-triage`
- **Needs Info** — `status: needs-info`
- **Ready for Agent** — `status: ready-for-agent` (the agent work queue)
- **Ready for Human** — `status: ready-for-human`
- **Done** — completed, still in project
- **Archived** — done, ready to move to vault

## Wayfinding operations

Used by `/wayfinder`. The **map** is a file with one **child** file per ticket.

- **Map**: `.scratch/<effort>/map.md` — the Notes / Decisions-so-far / Fog body.
- **Child ticket**: `.scratch/<effort>/issues/NN-<slug>.md`, numbered from `01`, with the question in the body. A `type` frontmatter property records the ticket type (`research`/`prototype`/`grilling`/`task`); the `status` property records `claimed`/`resolved`.
- **Blocking**: a `blocked-by` frontmatter property listing issue numbers (`["01", "02"]`). A ticket is unblocked when every file it lists is `resolved`.
- **Frontier**: scan `.scratch/<effort>/issues/` for files that are open, unblocked, and unclaimed; first by number wins.
- **Claim**: set `status: claimed` in frontmatter and save before any work.
- **Resolve**: append the answer under an `## Answer` heading, set `status: resolved`, then append a context pointer (gist + link) to the map's Decisions-so-far in `map.md`.
