# syntax=docker/dockerfile:1
# Web UI (React + Bun): serves the app and proxies /api to the control plane.
# Build from the repo root: docker build -f deploy/images/web/Dockerfile .
FROM oven/bun:1.4.4-slim
WORKDIR /app
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile --production
COPY web/index.html web/server.ts web/tsconfig.json ./
COPY web/src ./src
USER 1000:1000
ENV PORT=3000
EXPOSE 3000
CMD ["bun", "server.ts"]
