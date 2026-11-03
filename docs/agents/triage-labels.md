# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's issue tracker.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

## Repo-specific labels

| Label  | Meaning                             |
| ------ | ----------------------------------- |
| `done` | Implemented and closed as completed |

When an issue is closed because its work is finished (not `wontfix`), apply `done` and remove `ready-for-agent` / `ready-for-human` / `needs-triage` / `needs-info` in the same edit:
`gh issue edit <n> --add-label done --remove-label ready-for-agent` then `gh issue close <n> --reason completed`.
Never apply `done` together with `wontfix`.
