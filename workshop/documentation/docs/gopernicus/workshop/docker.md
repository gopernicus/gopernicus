---
sidebar_position: 4
title: Docker
---

# Workshop — Docker

`workshop/docker/` is where Dockerfiles for deploying your application live. Gopernicus bootstraps a production-ready `Dockerfile` for your main server on `gopernicus init` — it is intentionally basic and should be configured to your own needs.

Any additional applications in `app/` (workers, secondary servers, etc.) can define their own Dockerfiles here alongside the main one.

## Bootstrapped Dockerfile

The generated Dockerfile uses a two-stage build: a Go build stage that compiles the binary, and a minimal Alpine runtime stage that runs it. It is intentionally simple — update the base image versions, exposed ports, and any runtime dependencies to match your own needs.

Notable conventions in the bootstrapped file:
- `CGO_ENABLED=0` produces a fully static binary with no libc dependency
- Runs as an unprivileged user (`appsuser`) rather than root
- `BUILD_REF` and `BUILD_DATE` are passed as build args so the image is traceable back to a commit and build time
