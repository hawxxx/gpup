FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.22-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /gpup ./cmd/gpup

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && addgroup -g 10001 gpup && adduser -D -u 10001 -G gpup gpup && mkdir /data && chown gpup:gpup /data
COPY --from=backend /gpup /usr/local/bin/gpup
USER 10001:10001
EXPOSE 7331
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/gpup"]
CMD ["serve", "--listen", "0.0.0.0:7331", "--db", "/data/gpup.db"]
