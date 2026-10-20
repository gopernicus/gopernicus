---
title: Host contract
description: What a host is, how a host pocket differs from a framework pocket, and the fifteen H-rules a host is held to.
---

# Host contract

A **host** is an application that composes `sdk` + `integrations` + `pockets` in a `cmd/` composition root. The `examples/*` modules are hosts, and so is every production application built on this framework.

The canonical, normative text is the repository's `examples/README.md` — [Hosts — the gopernicus host contract](https://github.com/gopernicus/gopernicus/blob/main/examples/README.md). It carries the layout tree, the fifteen rules in full, the inbound anatomy, the host-pocket-versus-domain test, the `cmd`/`workshop`/`integrations`/`sdk` definitions, and the recipe for retrofitting an existing host. This page is the published pointer to it, plus the rule sentences themselves so they can be cited without leaving the site.

## Two scopes, one word

The word *pocket* appears at two scopes, and the import path — never a second noun — says which:

- a **framework pocket** is an imported module under `github.com/gopernicus/gopernicus/pockets/<name>`: the pluggable, datastore-free domain module of the [Pocket contract](pocket-contract.md);
- a **host pocket** is *this application's* local wrap, extension, or bridge of one or more framework pockets, under `<host-module>/pockets/<name>`, laid out as its own hexagon (`logic/`, `inbound/`, `outbound/` — any non-empty subset).

A thin host has no local `pockets/` at all. A host pocket exists only when the host writes code against a pocket's ports or rim; mounting a framework pocket with its bundled store and views is `cmd` wiring and nothing else.

The reference host is **gps-360-go**, a private production application built on this framework, whose layout the rules generalize from. Where the contract and the reference disagree, the disagreement is a finding to be settled, not a style choice.

## The fifteen rules

Rule ids are the **H series** (H = host), H0 through H14. They police a host; the repository's own Makefile `G` guards police the framework. A `gopernicus guard` command that checks a host against these rules is planned for `workshop/gopernicus` v0.4.0 and has not shipped.

Bare `sdk` always means the framework's module; the host's own top-level roots are always written "the host's `sdk/`", "the host's `integrations/`", and "the host's `ui/`".

| id | the rule, in one line |
|---|---|
| **H0** | A host's production Go packages live only under `cmd/`, `sdk/`, `integrations/`, `internal/{logic,inbound,outbound}`, `pockets/<name>/{logic,inbound,outbound}`, `ui/`, or `workshop/`, and at least one package sits under `cmd/<binary>/`. |
| **H1** | `internal/logic/**` imports only the standard library, framework `sdk/...`, the host's own `sdk/...`, and its own `internal/logic/...` — an allow-list, with no escape valve. |
| **H2** | A logic domain never imports a composition, and never imports another logic domain. |
| **H3** | A composition declares a `Dependencies` struct whose every field is a non-empty `*Service` interface declared in that same package, imports at least one logic domain, and holds no repository port, no storage import, and no transaction handle. |
| **H4** | `internal/inbound/**` may reach `internal/logic`, framework and host `sdk`, framework pocket cores and their `views/<pkg>`, `ui/*`, and a host pocket's `logic` and `inbound` — never `internal/outbound`, the host's `integrations/`, or a host pocket's `outbound`. |
| **H5** | `internal/outbound/domains/<d>` imports, among host packages, only `internal/logic/domains/<d>`, the host's `sdk/`, and host pockets — never inbound, never another logic domain, never the host's `integrations/` (it declares the narrow port; `cmd` injects the integration). |
| **H6** | A host pocket extends at least one framework pocket, its `logic` stays on stdlib + `sdk` + the host's `sdk/` + framework pocket rims, and no part of it imports `internal/*` or another host pocket. |
| **H7** | Only `cmd/**`, `internal/outbound/**`, the host's `integrations/**`, a host pocket's `outbound/**`, and `workshop/**` may import a framework pocket's `stores/*`, a framework integration, or a recognized database driver. |
| **H8** | Every `internal/inbound/domains/<d>`, `internal/inbound/compositions/<c>`, and `internal/outbound/domains/<d>` has its `internal/logic` counterpart — the implication runs one way only. |
| **H9** | `.Underlying()` is called nowhere outside the host's `integrations/` and `workshop/`, and `RowToStructByNameLax` appears nowhere. |
| **H10** | In production files, `cmd/**` contains no SQL statement literal, and a composition root under `cmd/<binary>/` declares no interface type; a client binary — a `cmd/<binary>/` tree that imports nothing under `internal/`, `pockets/`, or the host's `integrations/`, and no framework pocket, store, integration, or database driver — may own its commands, API client, wire types, and interfaces. |
| **H11** | A host integration `integrations/<capability>[/<vendor>]` holds no domain: it imports only the standard library, framework `sdk/...`, the host's `sdk/...`, and exactly one connector or vendor client library plus the companion modules its API forces; it never imports `internal/*` or a pocket of either scope, and only `cmd/**` and `workshop/**` import it — tests included. |
| **H12** | The outer fence, which H1, H4, H6 and H7 tighten and never loosen: `internal/**` and `pockets/**`, tests included, import only the standard library, the host module, and the framework's `sdk`, `pockets`, `integrations` and `ui` modules; recognized database drivers are additionally allowed under `internal/outbound/**` and a host pocket's `outbound/**`, recognized view technology under `internal/inbound/**` and a host pocket's `inbound/**`; production files never import the host's `workshop/`. |
| **H13** | The host's `sdk/**` imports only the standard library, framework `sdk/...`, and its own `sdk/...`, and holds no domain entity, repository port, or `Service` — a type two domains both need is still each domain's own. |
| **H14** | The host's `ui/**` imports only the standard library, framework `sdk/...`, framework `ui/*`, recognized view technology, the host's `sdk/...`, and its own `ui/...` — never `internal/*`, a pocket of either scope, or an integration of either scope. |

Each rule's normative discussion — what it allows, which files are exempt, and why — lives in the contract itself. The layout tree these rules describe is section 2 there; the retrofit recipe for an existing host is section 7.

Host-local domains that are not pockets follow [Hexagonal host applications](hexagonal-apps.md); a capability worth extracting into a framework pocket graduates under the [Pocket contract](pocket-contract.md). Which in-repo examples are held to this contract is listed on the [Worked examples](../examples.md) page.
