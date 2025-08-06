FROM golang:1.23-alpine AS builder

# git 설치 (go mod tidy에 필요)
RUN apk add --no-cache git

WORKDIR /app

# go.mod 복사
COPY go.mod* go.sum* ./

# 소스 파일들 복사 (의존성 해결을 위해)
COPY main.go ./
COPY handlers/ ./handlers/
COPY models/ ./models/
COPY config/ ./config/
COPY middleware/ ./middleware/

# 의존성 다운로드
RUN go mod tidy && go mod download

# 빌드
RUN go build -o main .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .

# Cloud Run은 PORT 환경변수를 사용
EXPOSE 8080
CMD ["./main"]
