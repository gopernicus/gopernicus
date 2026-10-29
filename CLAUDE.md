# Working in gopernicus

This is a public repository. These rules apply to every tracked file, code
comment, commit message, PR title or body, issue, and release note.

## Never name a host application

- Never name a private application that consumes gopernicus, or its
  organization, brand, repository, GitHub org, or local checkout. Write vaguely
  instead: "a host", "a host requested", "a host requires", "a multi-tenant
  host", "a roles-only host".
- Never cite a host's internal flag, ticket, phase, or plan IDs, or link a
  host's repository or issues. State the requirement itself.
- Test fixtures and examples use neutral names such as `acme`, `globex`,
  `Example Co`, and `app`.
- Host hand-offs live in the host's own repository, never here.
- Never edit a shipped migration to remove a name. The migration runners
  checksum each file's bytes, so any edit breaks hosts that already applied it.

## Plans stay local

Plans live in the gitignored `.claude/plans/`. Never commit a `plans/`
directory, and never link a plan file from tracked docs or code.
