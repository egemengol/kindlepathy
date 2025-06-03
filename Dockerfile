# Stage 1: Build Readability binary
FROM oven/bun:alpine AS readability_builder
WORKDIR /app
COPY readability/package.json ./
COPY readability/bun.lock ./
COPY readability/tsconfig.json ./
RUN bun install --frozen-lockfile --production
COPY readability/server.ts ./
RUN bun build --compile --minify --sourcemap server.ts --outfile readability

# Stage 2: Build Go binary
FROM golang:1.24 AS go_builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -o ./out ./cmd

# Stage 3: Final stage
FROM alpine:latest
WORKDIR /app

RUN apk add ca-certificates libstdc++

COPY --from=readability_builder /app/readability ./readability
# https://github.com/jsdom/jsdom/issues/3511
COPY --from=readability_builder /app/node_modules/jsdom/lib/jsdom/living/xhr/xhr-sync-worker.js ./node_modules/jsdom/lib/jsdom/living/xhr/xhr-sync-worker.js
COPY --from=go_builder /app/out ./server

COPY migrations ./migrations
COPY web ./web

ENV READABILITY_PATH=/app/readability

RUN chmod +x ./readability ./server
# ENTRYPOINT ["ls", "-al"]
ENTRYPOINT ["./server"]
