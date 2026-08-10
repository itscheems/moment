FROM node:24.19.0-alpine AS web
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@10.34.5 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
COPY internal/webassets/ /src/internal/webassets/
RUN pnpm build

FROM golang:1.25.12-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webassets/dist ./internal/webassets/dist
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/moment/moment/internal/buildinfo.Version=${VERSION} -X github.com/moment/moment/internal/buildinfo.Commit=${COMMIT} -X github.com/moment/moment/internal/buildinfo.BuildDate=${BUILD_DATE}" \
    -o /out/moment ./cmd/moment

FROM gcr.io/distroless/static-debian12:nonroot
ENV MOMENT_DATA_DIR=/data
WORKDIR /
COPY --from=build /out/moment /moment
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/moment"]
CMD ["version", "--json"]
