# workshop/dev/firebase

A local Firebase **Auth emulator** for development and for the
`integrations/authentication/firebase` `-tags=emulator` leg. Auth only, on
`127.0.0.1:9099`, under the `demo-gopernicus` project: a `demo-` project id
needs no real Firebase project and no credentials.

```sh
make dev-firebase        # build (pinned) and start; waits until it answers
export FIREBASE_AUTH_EMULATOR_HOST=127.0.0.1:9099 FIREBASE_PROJECT_ID=demo-gopernicus
cd integrations/authentication/firebase && go test -tags=emulator ./...
make dev-firebase-down   # stop and remove the container
```

| File | Purpose |
|---|---|
| `Dockerfile` | `node:22.23.3-alpine3.24` + `firebase-tools@15.34.0`, both pinned. The Auth emulator is pure Node, so no JRE. |
| `firebase.json` | Auth emulator only, bound to `0.0.0.0:9099`, UI off, single project. |
| `docker-compose.yml` | Runs only that service, publishes `127.0.0.1:9099`, health-checked. |

The emulator issues **unsigned** tokens (`alg: none`), and the Admin SDK
accepts them without any signature check whenever `FIREBASE_AUTH_EMULATOR_HOST`
is set. Never set that variable in production; the firebase providers refuse to
construct in production mode while it is set. Users, sign-ins, SMS codes and
revocations go through the emulator's Identity Toolkit REST API
(`http://127.0.0.1:9099/identitytoolkit.googleapis.com/v1/...`; admin calls take
`Authorization: Bearer owner`; SMS codes are listed at
`/emulator/v1/projects/demo-gopernicus/verificationCodes`). State is in memory
and is lost when the container stops.

To bump firebase-tools or the base image, change the Dockerfile and the image
tag in `docker-compose.yml` and the Makefile comment together, then re-run the
emulator leg.
