FROM golang:1.24-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /belay ./cmd/belay

FROM alpine:3.20

RUN apk add --no-cache sqlite-libs ca-certificates

COPY --from=builder /belay /usr/local/bin/belay

ENTRYPOINT ["belay"]
