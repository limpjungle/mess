FROM golang:1.26.6-alpine3.24 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main .
FROM alpine:3.24
RUN apk add --no-cache openssl bash
WORKDIR /root/
COPY --from=builder /app/main .
COPY --from=builder /app/scripts/gen-certs.sh ./scripts/gen-certs.sh
RUN chmod +x ./scripts/gen-certs.sh
EXPOSE 8443
CMD [ "bin/sh", "c", "./scripts/gen-certs.sh && ./main" ]
