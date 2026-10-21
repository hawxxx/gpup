# syntax=docker/dockerfile:1
# Build stages run on the native builder platform and cross-compile, so multi-arch release builds avoid emulation.
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.9-alpine AS backend
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /gpup ./cmd/gpup

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/hawxxx/gpup"
RUN apk add --no-cache ca-certificates && addgroup -g 10001 gpup && adduser -D -u 10001 -G gpup gpup && mkdir /data && chown gpup:gpup /data
COPY --from=backend /gpup /usr/local/bin/gpup
USER 10001:10001
EXPOSE 7331
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/gpup"]
CMD ["serve", "--listen", "0.0.0.0:7331", "--db", "/data/gpup.db"]
