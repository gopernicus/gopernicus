# Gopernicus documentation

This Docusaurus site documents the repository as it exists on `main`. Its
content is organized around the package dependency direction: SDK, feature
cores, sibling adapters, integrations, UI, and host-owned composition.

Gopernicus is open source under the [MIT License](../../LICENSE). It is very
much a work in progress and should not be treated as stable.

## Local development

From the repository root:

```sh
make docs-install
make docs
```

Open <http://localhost:3000/gopernicus/>. To verify the production artifact:

```sh
make docs-build
```

Or run the equivalent `pnpm` commands from this directory. Node 20 or newer and
pnpm 11.8.0 are required.

## Content map

- `docs/` contains the documentation corpus and sidebar metadata.
- `src/pages/` contains the custom landing page.
- `src/css/custom.css` contains the visual system and Docusaurus overrides.
- `static/img/` contains the approved Gopernicus artwork used by the site.
- `sidebars.ts` is the primary information architecture.
- `docusaurus.config.ts` owns deployment paths, navigation, and strict link checks.

Prefer executable examples and public package APIs as sources of truth. When
code and prose disagree, update the prose in the same change as the behavior.
Describe planned work as planned work, and keep roadmap notes separate from
the reference for shipped APIs and commands.
