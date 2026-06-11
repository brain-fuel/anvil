# Build: docker build -f deploy/Dockerfile -t anvil .
# Run:   docker run -p 8080:8080 -v $PWD/anvil.json:/etc/anvil/anvil.json:ro anvil
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ARG VERSION=dev
ARG POLAR_ORG_ID=
ARG BUY_URL=
RUN set -eux; \
    LDFLAGS="-s -w -X main.version=${VERSION}"; \
    [ -n "$POLAR_ORG_ID" ] && LDFLAGS="$LDFLAGS -X goforge.dev/anvil/license.OrgID=${POLAR_ORG_ID}"; \
    [ -n "$BUY_URL" ] && LDFLAGS="$LDFLAGS -X goforge.dev/anvil/license.BuyURL=${BUY_URL}"; \
    CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o /anvil ./cmd/anvil

FROM scratch
# TLS roots for calendar providers and the license API; zones are embedded
# in the binary (time/tzdata).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /anvil /anvil
# License state lives here; mount a volume to persist activation.
ENV HOME=/data
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/anvil"]
CMD ["serve", "-config", "/etc/anvil/anvil.json"]
