import type {ReactNode} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

const principles = [
  ['Stdlib kernel', 'The SDK stays small, portable, and free of third-party dependencies.'],
  ['Explicit composition', 'The host chooses features, connectors, middleware, and lifecycle.'],
  ['UI optional', 'Use the API directly, connect a separate web client, or add a Go UI package.'],
  ['Datastore optional', 'Feature cores depend on ports. Choose PostgreSQL, Turso, memory, or your own store.'],
];

const modules = [
  {
    label: '01 · SDK',
    title: 'Contracts and mechanisms',
    body: 'Kernel errors and context, foundation mechanics, behavioral capabilities, and the feature-mount contract.',
    link: '/docs/sdk/overview',
    linkLabel: 'Read the SDK',
  },
  {
    label: '02 · Features',
    title: 'Reusable domain modules',
    body: 'Authentication, authorization, CMS, events, and jobs expose ports and host seams without selecting a datastore for you.',
    link: '/docs/features/overview',
    linkLabel: 'Read feature contracts',
  },
  {
    label: '03 · Integrations',
    title: 'Technology at the edge',
    body: 'Drivers and vendor contracts live in separate modules: pgx, libSQL, Redis, OpenTelemetry, OAuth, storage, and more.',
    link: '/docs/integrations/catalog',
    linkLabel: 'Browse integrations',
  },
];

const illustrations = [
  {
    image: 'img/gopernicuswriting.jpg',
    alt: 'Gopernicus character writing at a desk',
    title: 'Application code remains yours',
    body: 'The host owns app-local domains, policies, routes, and composition.',
  },
  {
    image: 'img/gopernicusmap.jpg',
    alt: 'Gopernicus character studying a map',
    title: 'Dependencies have a direction',
    body: 'Ports sit inward; stores, providers, and UI adapters sit at the boundary.',
  },
  {
    image: 'img/cryptid.jpg',
    alt: 'Gopernicus character meeting a forest cryptid',
    title: 'Integrations stay replaceable',
    body: 'Concrete vendors are selected by the host and isolated in their own modules.',
  },
];

export default function Home(): ReactNode {
  return (
    <Layout
      title="Gopernicus documentation"
      description="Documentation for Gopernicus: SDK contracts, feature modules, integrations, and optional UI clients."
    >
      <main>
        <section className={styles.hero}>
          <div className={styles.heroGlow} />
          <div className={`container ${styles.heroGrid}`}>
            <div className={styles.heroCopy}>
              <div className={styles.eyebrow}>
                <span className={styles.signal} />
                Gopernicus · Go packages
              </div>
              <Heading as="h1">
                Explicit composition for Go applications.
                <span> APIs, features, and optional UI.</span>
              </Heading>
              <p className={styles.lead}>
                Gopernicus provides SDK contracts, reusable feature modules, and
                isolated integrations. A host chooses what it needs and owns the
                composition root.
              </p>
              <div className={styles.actions}>
                <Link className={styles.primaryButton} to="/docs/getting-started/quickstart">
                  Start with an example
                  <span aria-hidden="true">→</span>
                </Link>
                <Link className={styles.secondaryButton} to="/docs/architecture/overview">
                  Read the architecture
                </Link>
              </div>
              <div className={styles.installLine}>
                <span>$</span>
                <code>cd examples/auth-cms &amp;&amp; go run ./cmd/server</code>
              </div>
            </div>
            <div className={styles.heroArt}>
              <img
                src="img/gopernicussimpleicon.png"
                alt="Gopernicus character holding an orbit model"
              />
              <span>SDK · features · integrations · hosts</span>
            </div>
          </div>
        </section>

        <section className={styles.principleBand} aria-label="Package principles">
          <div className="container">
            <div className={styles.principleGrid}>
              {principles.map(([title, body]) => (
                <div className={styles.principle} key={title}>
                  <strong>{title}</strong>
                  <span>{body}</span>
                </div>
              ))}
            </div>
          </div>
        </section>

        <section className={`${styles.section} ${styles.illustrationSection}`}>
          <div className="container">
            <div className={styles.sectionHeading}>
              <div>
                <span className={styles.kicker}>A working vocabulary</span>
                <Heading as="h2">Package structure in practice.</Heading>
              </div>
              <p>
                The package layout becomes practical at the host boundary:
                application code stays local, dependencies point inward, and
                integrations remain replaceable.
              </p>
            </div>
            <div className={styles.illustrationGrid}>
              {illustrations.map((item) => (
                <figure className={styles.illustrationCard} key={item.title}>
                  <img src={item.image} alt={item.alt} />
                  <figcaption>
                    <Heading as="h3">{item.title}</Heading>
                    <p>{item.body}</p>
                  </figcaption>
                </figure>
              ))}
            </div>
          </div>
        </section>

        <section className={styles.section}>
          <div className="container">
            <div className={styles.sectionHeading}>
              <div>
                <span className={styles.kicker}>Repository structure</span>
                <Heading as="h2">Contracts, modules, composition.</Heading>
              </div>
              <p>
                Gopernicus keeps policy and mechanism separate from concrete
                technology. The boundaries are visible in package paths and Go
                modules.
              </p>
            </div>
            <div className={styles.moduleGrid}>
              {modules.map((module) => (
                <article className={styles.moduleCard} key={module.title}>
                  <span>{module.label}</span>
                  <Heading as="h3">{module.title}</Heading>
                  <p>{module.body}</p>
                  <Link to={module.link}>{module.linkLabel} →</Link>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section className={`${styles.section} ${styles.changeSection}`}>
          <div className={`container ${styles.changeGrid}`}>
            <div>
              <span className={styles.kicker}>Composition choices</span>
              <Heading as="h2">Use the boundary that fits the application.</Heading>
              <p className={styles.changeLead}>
                An API host can stand on its own. A separate React application can
                consume it. A host that needs Go-rendered pages can add the
                optional GOTH packages.
              </p>
              <Link className={styles.textLink} to="/docs/ui/react">
                Read the React and TanStack example →
              </Link>
            </div>
            <div className={styles.diffPanel}>
              <div className={styles.diffHeader}>
                <span>Application shape</span>
                <span>Composition</span>
              </div>
              <div className={styles.diffRow}>
                <strong>API only</strong>
                <span>SDK web + feature routes + JSON/OpenAPI</span>
              </div>
              <div className={styles.diffRow}>
                <strong>API + React</strong>
                <span>Go API + React/TanStack client application</span>
              </div>
              <div className={styles.diffRow}>
                <strong>API + Go UI</strong>
                <span>Go API + optional GOTH and feature view adapters</span>
              </div>
              <div className={styles.diffRow}>
                <strong>Generated code</strong>
                <span>Roadmap work documented separately from current commands</span>
              </div>
            </div>
          </div>
        </section>

        <section className={styles.ctaSection}>
          <div className={`container ${styles.cta}`}>
            <div>
              <span className={styles.kicker}>Start with the contracts</span>
              <Heading as="h2">Run an example, then choose your UI.</Heading>
              <p>Read the host composition, feature ports, and client options.</p>
            </div>
            <Link className={styles.primaryButton} to="/docs/getting-started/quickstart">
              Open the quickstart <span aria-hidden="true">→</span>
            </Link>
          </div>
        </section>
      </main>
    </Layout>
  );
}
