import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const section = (value: string) => ({
  type: 'html' as const,
  value,
  className: 'sidebar-section-title',
});

const sidebars: SidebarsConfig = {
  docsSidebar: [
    section('Orientation'),
    'intro',
    {
      type: 'category',
      label: 'Getting started',
      link: {type: 'generated-index', title: 'Getting started'},
      items: [
        'getting-started/quickstart',
        'getting-started/choose-your-path',
      ],
    },
    'examples',

    section('The model'),
    {
      type: 'category',
      label: 'Architecture',
      link: {type: 'doc', id: 'architecture/overview'},
      items: [
        'architecture/repository-layout',
        'architecture/hexagonal-apps',
        'architecture/pocket-contract',
        'architecture/host-contract',
      ],
    },

    section('Packages'),
    {
      type: 'category',
      label: 'SDK',
      link: {type: 'doc', id: 'sdk/overview'},
      items: [
        'sdk/foundation',
        'sdk/capabilities',
        'sdk/web',
      ],
    },
    {
      type: 'category',
      label: 'Pockets',
      link: {type: 'doc', id: 'pockets/overview'},
      items: [
        'pockets/authentication',
        'pockets/authorization',
        'pockets/cms',
        'pockets/events',
        'pockets/jobs',
      ],
    },
    'integrations/catalog',
    {
      type: 'category',
      label: 'UI',
      link: {type: 'doc', id: 'ui/overview'},
      items: ['ui/goth', 'ui/react'],
    },

    section('Developer workflow'),
    {
      type: 'category',
      label: 'Workshop CLI',
      link: {type: 'doc', id: 'workshop/overview'},
      items: ['workshop/commands'],
    },
    {
      type: 'category',
      label: 'Guides',
      link: {type: 'generated-index', title: 'Guides'},
      items: [
        'guides/compose-host',
        'guides/create-pocket',
        'guides/persistence',
        'guides/testing',
      ],
    },
    'project-status',
  ],
};

export default sidebars;
