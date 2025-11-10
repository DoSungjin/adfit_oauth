FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod* go.sum* ./
COPY main.go ./
COPY handlers/ ./handlers/
COPY models/ ./models/
COPY config/ ./config/
COPY middleware/ ./middleware/
COPY services/ ./services/

RUN go mod download || go mod tidy

RUN go build -o main main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .
EXPOSE 8080
CMD ["./main"]
