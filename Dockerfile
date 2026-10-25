FROM golang:1.26.5-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/abot ./cmd/abot

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates chromium ffmpeg git pandoc poppler-utils sqlite3 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 abot \
    && useradd --uid 10001 --gid 10001 --create-home --shell /usr/sbin/nologin abot \
    && mkdir -p /data /workspace \
    && chown -R 10001:10001 /data /workspace /home/abot
COPY --from=build /out/abot /usr/local/bin/abot
ENV HOME=/home/abot XDG_STATE_HOME=/data/state XDG_DATA_HOME=/data/data
USER 10001:10001
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/abot"]
CMD ["telegram", "serve"]
