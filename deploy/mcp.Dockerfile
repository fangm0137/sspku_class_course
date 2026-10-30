FROM golang:1.27-alpine AS build
WORKDIR /src
COPY mcp-server/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/mcp-server .

FROM alpine:3.22
RUN addgroup -g 10002 mcp && adduser -D -u 10002 -G mcp mcp
WORKDIR /app
COPY --from=build /out/mcp-server /app/mcp-server
USER mcp
EXPOSE 8090
ENTRYPOINT ["/app/mcp-server"]
