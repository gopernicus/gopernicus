import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'Gopernicus',
  tagline: 'Opinionated Go packages for explicit composition',
  favicon: 'img/gopernicussimpleicon.png',

  future: {v4: true},

  url: 'https://gopernicus.github.io',
  baseUrl: '/gopernicus/',
  organizationName: 'gopernicus',
  projectName: 'gopernicus',

  onBrokenLinks: 'throw',
  markdown: {
    format: 'md',
    hooks: {onBrokenMarkdownLinks: 'throw'},
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl:
            'https://github.com/gopernicus/gopernicus/edit/main/workshop/documentation/',
          showLastUpdateTime: true,
        },
        blog: false,
        theme: {customCss: './src/css/custom.css'},
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/gopernicussimpleicon.png',
    colorMode: {
      defaultMode: 'dark',
      respectPrefersColorScheme: true,
    },
    announcementBar: {
      id: 'work-in-progress',
      content:
        '<strong>Work in progress:</strong> Gopernicus is open source under the MIT License, but it is not stable. These docs follow <code>main</code>; APIs and module boundaries may change.',
      backgroundColor: '#d9b86c',
      textColor: '#142137',
      isCloseable: true,
    },
    navbar: {
      title: 'Gopernicus',
      logo: {alt: 'Gopernicus logo', src: 'img/gopernicussimpleicon.png'},
      hideOnScroll: true,
      items: [
        {
          to: '/docs/intro',
          label: 'Start',
          position: 'left',
          activeBaseRegex:
            '/docs/(?:intro(?:/|$)|getting-started(?:/|$)|examples(?:/|$)|project-status(?:/|$))',
        },
        {
          to: '/docs/architecture/overview',
          label: 'Architecture',
          position: 'left',
          activeBaseRegex: '/docs/architecture(?:/|$)',
        },
        {
          to: '/docs/sdk/overview',
          label: 'SDK',
          position: 'left',
          activeBaseRegex: '/docs/sdk(?:/|$)',
        },
        {
          to: '/docs/pockets/overview',
          label: 'Pockets',
          position: 'left',
          activeBaseRegex: '/docs/pockets(?:/|$)',
        },
        {
          to: '/docs/workshop/overview',
          label: 'Workshop',
          position: 'left',
          activeBaseRegex: '/docs/workshop(?:/|$)',
        },
        {
          href: 'https://pkg.go.dev/github.com/gopernicus/gopernicus/sdk',
          label: 'Go API',
          position: 'right',
        },
        {
          href: 'https://github.com/gopernicus/gopernicus',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Learn',
          items: [
            {label: 'Quickstart', to: '/docs/getting-started/quickstart'},
            {label: 'Architecture', to: '/docs/architecture/overview'},
            {label: 'Examples', to: '/docs/examples'},
          ],
        },
        {
          title: 'Build',
          items: [
            {label: 'SDK', to: '/docs/sdk/overview'},
            {label: 'Pockets', to: '/docs/pockets/overview'},
            {label: 'Workshop CLI', to: '/docs/workshop/commands'},
          ],
        },
        {
          title: 'Project',
          items: [
            {label: 'Status & scope', to: '/docs/project-status'},
            {
              label: 'Source',
              href: 'https://github.com/gopernicus/gopernicus',
            },
            {
              label: 'Go packages',
              href: 'https://pkg.go.dev/github.com/gopernicus/gopernicus/sdk',
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Gopernicus. Built with Docusaurus.<br/>The <a href="https://go.dev/wiki/Gopher">Go gopher</a> was designed by <a href="https://www.instagram.com/reneefrench/">Renée French</a> and is licensed under <a href="https://creativecommons.org/licenses/by/4.0/">CC BY 4.0</a>; these illustrations are modified adaptations.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.nightOwl,
      additionalLanguages: ['go', 'bash', 'sql', 'yaml', 'json'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
