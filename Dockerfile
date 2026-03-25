FROM golang:1.22-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /registry ./cmd/registry

FROM gcr.io/distroless/static-debian12
COPY --from=build /registry /registry
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/registry"]
CMD ["--addr", ":8080", "--data", "/data"]
