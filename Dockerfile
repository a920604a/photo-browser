FROM golang:1.26.7-bookworm AS test
RUN apt-get update && apt-get install -y --no-install-recommends libvips-tools libimage-exiftool-perl sqlite3 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./...

FROM test AS build
ARG TARGETARCH=amd64
ENV CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} GOAMD64=v1
RUN go build -trimpath -ldflags='-s -w' -o /out/photo-app ./cmd/photo-app

FROM build AS acceptance
RUN cp /out/photo-app /usr/local/bin/photo-app
ENV LANG=C.UTF-8

FROM build AS api-build
ARG TARGETARCH=amd64
ENV CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} GOAMD64=v1
RUN go build -trimpath -ldflags='-s -w' -o /out/testauth ./cmd/testauth

# api-acceptance reuses the `acceptance` stage (bookworm + libvips + sqlite3 +
# exiftool) so we don't need to pull an extra debian layer. Adds curl+jq so the
# acceptance script can talk to the compose stack, python3 so the dev-stack
# entrypoint can seed the allowlist, and drops testauth in.
FROM acceptance AS api-acceptance
RUN apt-get update && apt-get install -y --no-install-recommends curl jq python3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=api-build /out/testauth /usr/local/bin/testauth
# runs as root so it can populate mounted docker volumes; not for production.
ENV LANG=C.UTF-8

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libvips-tools && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/photo-app /usr/local/bin/photo-app
USER 65532:65532
ENTRYPOINT ["photo-app"]
