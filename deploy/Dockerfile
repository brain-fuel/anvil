# Build: docker build -f deploy/Dockerfile -t anvil .
# Run:   docker run -p 8080:8080 -v $PWD/anvil.json:/etc/anvil/anvil.json:ro anvil
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ARG VERSION=dev
RUN set -eux; \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /anvil ./cmd/anvil

FROM scratch
# TLS roots for calendar providers; zones are embedded in the binary
# (time/tzdata).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /anvil /anvil
EXPOSE 8080
ENTRYPOINT ["/anvil"]
CMD ["serve", "-config", "/etc/anvil/anvil.json"]
