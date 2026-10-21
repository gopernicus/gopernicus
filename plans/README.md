# plans/

This directory holds plans for work that is still open, plus host handoffs that have not been
applied yet. When the work is released, its plan copy comes here.

Executed plans, audits, release manifests and verification records from before 2026-10-20 were
pruned. They are preserved at the tag `archive/plans-2026-10-20`. `RELEASING.md`, `AUDIT.md`,
`NOTES.md` and some READMEs and code comments still cite `plans/<file>` paths. To read one:

```sh
git show archive/plans-2026-10-20:plans/<file>
git ls-tree --name-only archive/plans-2026-10-20 plans/   # the full list
```
