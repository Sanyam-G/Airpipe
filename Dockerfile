FROM golang:1.25-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X main.buildVersion=${VERSION}" -o /relay ./cmd/relay

FROM alpine:3.19
COPY --from=build /relay /relay
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO /dev/null "http://localhost:${PORT:-8080}/health" || exit 1
ENTRYPOINT ["/relay"]
