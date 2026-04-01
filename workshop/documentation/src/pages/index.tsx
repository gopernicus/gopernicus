import type { ReactNode } from "react";
import clsx from "clsx";
import Link from "@docusaurus/Link";
import useDocusaurusContext from "@docusaurus/useDocusaurusContext";
import Layout from "@theme/Layout";
import Heading from "@theme/Heading";

import styles from "./index.module.css";

function HomepageHeader() {
  const { siteConfig } = useDocusaurusContext();
  return (
    <header className={clsx("hero", styles.heroBanner)}>
      <div className="container">
        <img
          src="img/gopernicusicon.jpg"
          alt="Gopernicus"
          className={styles.heroLogo}
        />
        <Heading as="h1" className={styles.heroTitle}>
          {siteConfig.title}
        </Heading>
        <p className={styles.heroSubtitle}>
          A Go framework for building production APIs. Generate the boilerplate,
          keep the business logic.
        </p>
        <div className={styles.buttons}>
          <Link
            className="button button--lg"
            to="/docs/gopernicus/intro"
            style={{
              backgroundColor: "var(--ifm-color-accent)",
              color: "#192d50",
              border: "none",
              fontWeight: 600,
            }}
          >
            Get Started
          </Link>
          <Link
            className="button button--outline button--lg"
            to="/docs/gopernicus/design-philosophy"
            style={{
              marginLeft: "1rem",
              borderColor: "var(--ifm-color-accent)",
              color: "var(--ifm-color-accent)",
            }}
          >
            Design Philosophy
          </Link>
        </div>
      </div>
    </header>
  );
}

type FeatureItem = {
  title: string;
  img: string;
  description: ReactNode;
};

const FeatureList: FeatureItem[] = [
  {
    title: "Generate the Boring Parts",
    img: "img/gopernicuswriting.jpg",
    description: (
      <>
        Annotate your SQL and configure your routes in bridge.yml — Gopernicus
        generates repositories, HTTP handlers, fixtures, and tests. You focus on
        the logic that makes your app yours.
      </>
    ),
  },
  {
    title: "Layers That Kinda Make Sense",
    img: "img/gopernicusmap.jpg",
    description: (
      <>
        Code is organized into layers where alphabetical order defines the
        import rule. SDK, Infrastructure, Core, Bridge, App — each layer has a
        clear job and clean boundaries.
      </>
    ),
  },
  {
    title: "Decent Auth",
    img: "img/cryptid.jpg",
    description: (
      <>
        Authentication, relationship-based authorization, and invitations —
        declared in your bridge.yml config. OAuth, API keys, sessions, and
        fine-grained permissions so you can ship without reinventing the wheel.
      </>
    ),
  },
];

function Feature({ title, img, description }: FeatureItem) {
  return (
    <div className={clsx("col col--4")}>
      <div className="text--center">
        <img src={img} alt={title} className={styles.featureImg} />
      </div>
      <div className="text--center padding-horiz--md">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

export default function Home(): ReactNode {
  const { siteConfig } = useDocusaurusContext();
  return (
    <Layout
      title={siteConfig.title}
      description="A Go framework for building production APIs with code generation, hexagonal architecture, and built-in auth."
    >
      <HomepageHeader />
      <main>
        <section className={styles.features}>
          <div className="container">
            <div className="row">
              {FeatureList.map((props, idx) => (
                <Feature key={idx} {...props} />
              ))}
            </div>
          </div>
        </section>
      </main>
    </Layout>
  );
}
