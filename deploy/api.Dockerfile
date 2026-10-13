FROM golang:1.27-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/seed ./cmd/seed && \
    CGO_ENABLED=0 go build -trimpath -o /out/seed-materials ./cmd/seed-materials && \
    CGO_ENABLED=0 go build -trimpath -o /out/seed-skills ./cmd/seed-skills && \
    CGO_ENABLED=0 go build -trimpath -o /out/reindex ./cmd/reindex

FROM alpine:3.22
RUN addgroup -g 10001 campus && adduser -D -u 10001 -G campus campus && \
    mkdir -p /data/uploads /data/staging && chown -R campus:campus /data
WORKDIR /app
COPY --from=build /out/ /app/
USER campus
EXPOSE 8080
ENTRYPOINT ["/app/server"]
