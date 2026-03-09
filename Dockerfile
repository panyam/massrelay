FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /relay .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /relay /relay
EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:8787/health || exit 1
ENTRYPOINT ["/relay"]
