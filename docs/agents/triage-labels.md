# Triage Labels

Maps canonical triage roles to the `status` frontmatter values used in `.scratch/` ticket files.

| Canonical Role      | Frontmatter `status` value | Meaning                                  |
| ------------------- | -------------------------- | ---------------------------------------- |
| `needs-triage`      | `needs-triage`             | Maintainer needs to evaluate this issue  |
| `needs-info`        | `needs-info`               | Waiting on reporter for more information |
| `ready-for-agent`   | `ready-for-agent`          | Fully specified, ready for an AFK agent  |
| `ready-for-human`   | `ready-for-human`          | Requires human implementation            |
| `wontfix`           | `wontfix`                  | Will not be actioned                     |

When a skill mentions a role, set the `status` frontmatter property in the ticket's `.md` file to the corresponding value from this table.
