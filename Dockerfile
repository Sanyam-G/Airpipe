FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /relay ./cmd/relay

FROM alpine:3.19
COPY --from=build /relay /relay
EXPOSE 8080
ENTRYPOINT ["/relay"]
