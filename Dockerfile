FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o jiekou2api .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/jiekou2api .
COPY config.example.yaml config.yaml
RUN mkdir -p auths
EXPOSE 8080
ENTRYPOINT ["./jiekou2api"]
