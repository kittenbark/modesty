ARG VERSION_GOLANG="1.24"
ARG VERSION_ALPINE="3.21"

FROM golang:${VERSION_GOLANG}-alpine${VERSION_ALPINE} AS builder

WORKDIR /app

COPY go.* ./
RUN go mod download
COPY ./telegram ./telegram

RUN go build -o main ./telegram

FROM alpine:${VERSION_ALPINE}

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata && \
    update-ca-certificates

COPY --from=builder /app/main .

ENTRYPOINT ["./main"]