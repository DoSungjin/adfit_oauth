package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	
	"adfit-oauth/services"
)

func main() {
	// 환경 변수 로드
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	log.Println("🚀 AdFit 크론잡 스케줄러 시작")

	// StatsService 초기화
	statsService, err := services.NewStatsService()
	if err != nil {
		log.Fatalf("❌ StatsService 초기화 실패: %v", err)
	}

	// 크론 스케줄러 생성 (한국 시간대)
	c := cron.New(cron.WithSeconds())

	// 매시간 0분에 활성 대회 통계 업데이트
	_, err = c.AddFunc("0 0 * * * *", func() {
		log.Println("⏰ [매시간] 활성 대회 통계 업데이트 시작")
		if err := statsService.UpdateAllActiveCompetitions(); err != nil {
			log.Printf("❌ 활성 대회 통계 업데이트 실패: %v", err)
		}
	})
	if err != nil {
		log.Fatalf("❌ 매시간 크론잡 등록 실패: %v", err)
	}

	// 매일 오전 2시에 전체 시스템 통계 업데이트
	_, err = c.AddFunc("0 0 2 * * *", func() {
		log.Println("⏰ [매일] 전체 시스템 통계 업데이트 시작")
		if err := statsService.UpdateDailySystemStats(); err != nil {
			log.Printf("❌ 시스템 통계 업데이트 실패: %v", err)
		}
	})
	if err != nil {
		log.Printf("⚠️ 일별 크론잡 등록 실패: %v", err)
	}

	// 크론 시작
	c.Start()
	log.Println("✅ 크론잡 스케줄러 실행 중...")

	// 크론잡 목록 출력
	for i, entry := range c.Entries() {
		log.Printf("📅 크론잡 %d: 다음 실행 시간 %v", i+1, entry.Next)
	}

	// 프로그램 종료 신호 대기
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 종료 신호 받을 때까지 대기
	<-sigChan
	log.Println("🛑 크론잡 스케줄러 종료 중...")

	// 크론 정지
	c.Stop()
	log.Println("✅ 크론잡 스케줄러 종료 완료")
}
