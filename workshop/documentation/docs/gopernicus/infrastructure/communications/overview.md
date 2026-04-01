---
sidebar_position: 1
title: Overview
---

# Communications

The communications layer handles outbound messaging across delivery channels. Each channel is its own package with a `Client` interface, one or more backend implementations, and a compliance suite.

## Channels

| Package | Purpose |
|---|---|
| [Emailer](./emailer.md) | HTML/text email with templating, layouts, and branding |

## The Pattern

Each channel defines its own `Client` interface suited to that medium — email, SMS, and instant messaging have fundamentally different data requirements and there's no meaningful common abstraction between them.

If your domain needs to send an email, it depends on `emailer.Renderer`. If it needs SMS, it will depend on the SMS client interface. Channels are not interchangeable by design.
