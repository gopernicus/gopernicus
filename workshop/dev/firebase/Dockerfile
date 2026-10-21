# Firebase Auth emulator for local development and the
# integrations/authentication/firebase `-tags=emulator` leg. Both versions are
# pinned so a rebuild is reproducible; bump them together and re-run that leg.
# The Auth emulator is pure Node, so no JRE is installed.
FROM node:22.23.3-alpine3.24

RUN npm install --global --no-fund --no-audit firebase-tools@15.34.0 \
	&& npm cache clean --force

WORKDIR /srv/firebase
COPY firebase.json .

EXPOSE 9099

# A demo- project id needs no real Firebase project and no credentials.
ENV FIREBASE_PROJECT=demo-gopernicus
CMD ["sh", "-c", "exec firebase emulators:start --only auth --project \"$FIREBASE_PROJECT\""]
