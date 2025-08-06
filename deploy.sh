#!/bin/bash

# Cloud Run 배포 스크립트
PROJECT_ID="posted-app-c4ff5"
SERVICE_NAME="adfit-oauth-server"
REGION="asia-northeast3"
IMAGE_NAME="gcr.io/$PROJECT_ID/$SERVICE_NAME"

echo "🚀 AdFit OAuth Server 배포 시작..."

# 1. Docker 이미지 빌드
echo "📦 Docker 이미지 빌드 중..."
docker build -t $IMAGE_NAME .

# 2. Google Container Registry에 푸시
echo "☁️ GCR에 이미지 푸시 중..."
docker push $IMAGE_NAME

# 3. Cloud Run에 배포
echo "🔄 Cloud Run에 배포 중..."
gcloud run deploy $SERVICE_NAME \
  --image $IMAGE_NAME \
  --platform managed \
  --region $REGION \
  --allow-unauthenticated \
  --port 8080 \
  --memory 512Mi \
  --max-instances 10 \
  --set-env-vars "PORT=8080" \
  --project $PROJECT_ID

echo "✅ 배포 완료!"
echo "🌐 서비스 URL: https://$SERVICE_NAME-520676604613.$REGION.run.app"
