import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  mainSidebar: [
    {
      type: "html",
      value: "Hello",
      className: "sidebar-section-title",
    },
    "gopernicus/intro",
    "gopernicus/zero-to-api",
    "gopernicus/design-philosophy",
    {
      type: "html",
      value: "Layers",
      className: "sidebar-section-title",
    },
    {
      type: "category",
      label: "App",
      link: { type: "doc", id: "gopernicus/app/overview" },
      items: [],
    },
    {
      type: "category",
      label: "Bridge",
      link: { type: "doc", id: "gopernicus/bridge/overview" },
      items: [
        {
          type: "category",
          label: "Auth",
          link: { type: "generated-index" },
          items: [
            "gopernicus/bridge/auth/authentication",
            "gopernicus/bridge/auth/invitations",
          ],
        },
        "gopernicus/bridge/cases",
        "gopernicus/bridge/repositories",
        {
          type: "category",
          label: "Transit",
          link: { type: "generated-index" },
          items: [
            "gopernicus/bridge/middleware",
            "gopernicus/bridge/fop",
          ],
        },
      ],
    },
    {
      type: "category",
      label: "Core",
      link: { type: "doc", id: "gopernicus/core/overview" },
      items: [
        {
          type: "category",
          label: "Auth",
          link: { type: "generated-index" },
          items: [
            "gopernicus/core/auth/authentication",
            "gopernicus/core/auth/authorization",
            "gopernicus/core/auth/invitations",
          ],
        },
        "gopernicus/core/cases",
        "gopernicus/core/repositories",
      ],
    },
    {
      type: "category",
      label: "Infrastructure",
      link: { type: "doc", id: "gopernicus/infrastructure/overview" },
      items: [
        "gopernicus/infrastructure/cache",
        {
          type: "category",
          label: "Communications",
          link: { type: "doc", id: "gopernicus/infrastructure/communications/overview" },
          items: [
            "gopernicus/infrastructure/communications/emailer",
          ],
        },
        "gopernicus/infrastructure/cryptids",
        {
          type: "category",
          label: "Database",
          link: { type: "doc", id: "gopernicus/infrastructure/database/overview" },
          items: [
            "gopernicus/infrastructure/database/postgres/pgxdb",
            "gopernicus/infrastructure/database/sqlite/moderncdb",
            "gopernicus/infrastructure/database/kvstore/goredisdb",
          ],
        },
        "gopernicus/infrastructure/events",
        "gopernicus/infrastructure/httpc",
        "gopernicus/infrastructure/oauth",
        {
          type: "category",
          label: "Rate Limiting",
          link: { type: "doc", id: "gopernicus/infrastructure/rate-limiting/overview" },
          items: [
            "gopernicus/infrastructure/rate-limiting/limiters",
            "gopernicus/infrastructure/rate-limiting/throttler",
          ],
        },
        "gopernicus/infrastructure/storage",
        "gopernicus/infrastructure/tracing",
      ],
    },
    {
      type: "category",
      label: "SDK",
      link: { type: "doc", id: "gopernicus/sdk/overview" },
      items: [
        "gopernicus/sdk/async",
        "gopernicus/sdk/conversion",
        "gopernicus/sdk/environment",
        "gopernicus/sdk/errs",
        "gopernicus/sdk/fop",
        "gopernicus/sdk/logger",
        "gopernicus/sdk/validation",
        "gopernicus/sdk/web",
        "gopernicus/sdk/workers",
      ],
    },
    {
      type: "category",
      label: "Telemetry",
      link: { type: "doc", id: "gopernicus/telemetry/overview" },
      items: [],
    },
    {
      type: "category",
      label: "Workshop",
      link: { type: "doc", id: "gopernicus/workshop/overview" },
      items: [
        "gopernicus/workshop/migrations",
        "gopernicus/workshop/dev",
        "gopernicus/workshop/docker",
        "gopernicus/workshop/documentation",
        "gopernicus/workshop/testing",
        "gopernicus/workshop/makefile",
      ],
    },

    {
      type: "html",
      value: "Topics",
      className: "sidebar-section-title",
    },
    {
      type: "category",
      label: "Auth Overview",
      link: { type: "doc", id: "gopernicus/topics/auth" },
      items: [],
    },
    {
      type: "category",
      label: "Code Generation",
      link: { type: "doc", id: "gopernicus/topics/code-generation/overview" },
      items: [
        "gopernicus/topics/code-generation/annotations",
        "gopernicus/topics/code-generation/schema-conventions",
        "gopernicus/topics/code-generation/bridge-configuration",
      ],
    },
    {
      type: "category",
      label: "Extending Generated Code",
      link: { type: "doc", id: "gopernicus/topics/extending-generated-code" },
      items: [],
    },
    "gopernicus/topics/running-from-source",
  ],

  cliSidebar: [
    "cli/cheatsheet",
    "cli/init",
    "cli/generate",
    "cli/new",
    "cli/db",
    "cli/boot",
    "cli/doctor",
  ],

  guidesSidebar: [
    {
      type: "category",
      label: "Guides & Tutorials",
      link: { type: "generated-index" },
      items: [
        "guides/adding-new-entity",
        "guides/adding-use-case",
        "guides/adding-auth-to-entity",
        "guides/adding-adapter",
      ],
    },
    "roadmap/roadmap",
  ],
};

export default sidebars;
