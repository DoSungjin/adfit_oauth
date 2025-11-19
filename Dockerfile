FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# ⭐ 1단계: go.mod와 go.sum만 먼저 복사 (캐싱 최적화!)
COPY go.mod go.sum ./
RUN go mod download

# ⭐ 2단계: 소스 코드 복사 (모듈 다운로드는 캐시됨)
COPY main.go ./
COPY handlers/ ./handlers/
COPY models/ ./models/
COPY config/ ./config/
COPY middleware/ ./middleware/
COPY services/ ./services/

# ⭐ 3단계: 최적화된 빌드
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags='-w -s' -o main .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .
EXPOSE 8080
CMD ["./main"]
