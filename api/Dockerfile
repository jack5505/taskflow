# Этап 1: фронтенд
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# vite.config.ts пишет сборку в ../api/internal/web/dist — для образа переопределяем в /web/dist
RUN npx tsc -b && npx vite build --outDir /web/dist

# Этап 2: бинарник Go со встроенной статикой
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY api/go.mod api/go.sum ./
RUN go mod download
COPY api/ ./
COPY --from=web /web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/taskflow ./cmd/taskflow

# Этап 3: рантайм. tzdata нужен для часовых поясов (ТЗ §10.2).
FROM alpine:3.21
# /data/attachments — точка монтирования тома вложений (§3.9): пустой том наследует владельца app.
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app \
    && mkdir -p /data/attachments && chown app /data/attachments
WORKDIR /app
COPY --from=build /out/taskflow /app/taskflow
USER app
EXPOSE 8080
ENTRYPOINT ["/app/taskflow"]
CMD ["api"]
