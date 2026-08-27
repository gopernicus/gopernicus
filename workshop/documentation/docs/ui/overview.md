---
title: UI
description: Optional user-interface packages and client boundaries for Gopernicus applications.
slug: /ui
---

# UI

Gopernicus does not require one presentation runtime. A host can expose an API only, connect a separate React application, or add a Go UI package for pages and components.

| Option | Location | Use it when |
|---|---|---|
| API client | React and TanStack, or another client application | the browser application owns routing, state, and assets |
| Go UI | `ui/goth` plus pocket view adapters | the host should compose and serve HTML from Go |

UI packages sit outside pocket cores. They may provide components, tokens, controllers, renderers, or client conventions, but they do not own pocket schemas, persistence, or host lifecycle.

Read [GOTH UI](goth.md) for the optional Go presentation system or [React and TanStack clients](react.md) for an API-backed web application.
