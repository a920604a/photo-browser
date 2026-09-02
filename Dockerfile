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

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libvips-tools && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/photo-app /usr/local/bin/photo-app
USER 65532:65532
ENTRYPOINT ["photo-app"]
