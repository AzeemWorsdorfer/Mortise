## Agent skills

### Issue tracker

Tickets live as markdown files in `.scratch/` with frontmatter status properties. Displayed on the Obsidian kanban board via a vault symlink. When completed, archived to the Obsidian vault (`01_Archive/`). See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the five canonical triage roles as frontmatter `status` values: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout. Canonical ADRs and CONTEXT.md live in the Obsidian vault; this repo mirrors them. See `docs/agents/domain.md`.
