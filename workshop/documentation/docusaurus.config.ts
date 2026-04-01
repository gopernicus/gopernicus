import { themes as prismThemes } from "prism-react-renderer";
import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";

const config: Config = {
  title: "Gopernicus",
  tagline:
    "A Go framework for building production-ready APIs with code generation, hexagonal architecture, and built-in auth.",
  favicon: "img/gopernicussimpleicon.png",

  future: {
    v4: true,
  },

  url: "https://gopernicus.com",
  baseUrl: "/",

  organizationName: "gopernicus",
  projectName: "gopernicus",

  onBrokenLinks: "warn",

  markdown: {
    format: "md",
    hooks: {
      onBrokenMarkdownLinks: "warn",
    },
  },

  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },

  presets: [
    [
      "classic",
      {
        docs: {
          sidebarPath: "./sidebars.ts",
          editUrl: "https://github.com/gopernicus/gopernicus/tree/main/docs/",
        },
        blog: false,
        theme: {
          customCss: "./src/css/custom.css",
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: "img/gopernicusicon.jpg",
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: "Gopernicus",
      logo: {
        alt: "Gopernicus Logo",
        src: "img/gopernicussimpleicon.png",
      },
      items: [
        {
          type: "docSidebar",
          sidebarId: "mainSidebar",
          position: "left",
          label: "Docs",
        },
        {
          type: "docSidebar",
          sidebarId: "cliSidebar",
          position: "left",
          label: "CLI",
        },
        {
          type: "docSidebar",
          sidebarId: "guidesSidebar",
          position: "left",
          label: "Guides",
        },
        {
          href: "https://github.com/gopernicus/gopernicus",
          label: "GitHub",
          position: "right",
        },
      ],
    },
    footer: {
      style: "dark",
      links: [
        {
          title: "Learn",
          items: [
            { label: "Getting Started", to: "/docs/gopernicus/intro" },
            {
              label: "Design Philosophy",
              to: "/docs/gopernicus/design-philosophy",
            },
            {
              label: "Zero to API",
              to: "/docs/gopernicus/zero-to-api",
            },
          ],
        },
        {
          title: "Reference",
          items: [
            { label: "CLI", to: "/docs/cli/cheatsheet" },
            {
              label: "Code Generation",
              to: "/docs/gopernicus/topics/code-generation/overview",
            },
            { label: "Guides", to: "/docs/guides/adding-new-entity" },
          ],
        },
        {
          title: "Community",
          items: [
            {
              label: "GitHub",
              href: "https://github.com/gopernicus/gopernicus",
            },
            { label: "Roadmap", to: "/docs/roadmap" },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Gopernicus. Built with Docusaurus.<br/>The Go gopher was designed by <a href="https://reneefrench.blogspot.com/" target="_blank">Renee French</a> and is licensed under the <a href="https://creativecommons.org/licenses/by/4.0/" target="_blank">Creative Commons 4.0 Attribution License</a>.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ["go", "bash", "sql", "yaml"],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
