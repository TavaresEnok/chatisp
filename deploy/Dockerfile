# Imagens do Compose. O PostgreSQL de teste não entra no binário.
FROM node:24.22.1-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
COPY backend/api/openapi.json /src/backend/api/openapi.json
RUN npm run build

FROM golang:1.27.3-bookworm AS api
WORKDIR /src/backend
COPY backend/ ./
ENV CGO_ENABLED=0
RUN go build -mod=vendor -trimpath -o /out/chatisp ./cmd/chatisp

FROM debian:13-slim AS runtime
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl ffmpeg \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --uid 1000 --create-home --shell /usr/sbin/nologin chatisp
COPY --from=api /out/chatisp /usr/local/bin/chatisp
COPY backend/db/migrations /usr/share/chatisp/migrations
USER 1000
EXPOSE 8080 9090
ENTRYPOINT ["chatisp"]

FROM caddy:2.10.2 AS edge
COPY --from=web /src/web/dist /srv/panel
COPY deploy/Caddyfile /etc/caddy/Caddyfile
