FROM node:24.19.0-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS web
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@10.34.5 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
COPY internal/webassets/ /src/internal/webassets/
RUN pnpm build

FROM golang:1.25.12-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webassets/dist ./internal/webassets/dist
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X github.com/moment/moment/internal/buildinfo.Version=${VERSION} -X github.com/moment/moment/internal/buildinfo.Commit=${COMMIT} -X github.com/moment/moment/internal/buildinfo.BuildDate=${BUILD_DATE}" \
    -o /out/moment ./cmd/moment

FROM alpine:3.23.5@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
RUN apk add --no-cache ffmpeg libheif-tools \
    && addgroup -S -g 65532 moment \
    && adduser -S -D -H -u 65532 -G moment moment \
    && mkdir -p /data \
    && chown 65532:65532 /data
WORKDIR /
COPY --from=build /out/moment /moment
COPY THIRD_PARTY_NOTICES.md THIRD_PARTY_LICENSES.txt /licenses/
VOLUME ["/data"]
EXPOSE 8080
USER 65532:65532
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD ["wget", "--spider", "-q", "http://127.0.0.1:8080/readyz"]
ENTRYPOINT ["/moment"]
CMD ["serve", "--data-dir", "/data", "--listen", "0.0.0.0:8080", "--log-format", "json"]
