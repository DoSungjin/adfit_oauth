package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/robfig/cron/v3"
	
	"adfit-oauth/config"
	"adfit-oauth/services"
)

func main() {
	// 설정 파일 로드
	if err := config.LoadConfig("../config/app_config.yaml"); err != nil {
		log.Fatalf("❌ 설정 로드 실패: %v", err)
	}

	// 크론잡 기능이 비활성화되어 있으면 종료
	if !config.IsFeatureEnabled("cron") {
		log.Println("⚠️ 크론잡 기능이 비활성화되어 있습니다")
		return
	}

	log.Printf("🚀 %s 크론잡 스케줄러 시작 (환경: %s)", 
		config.Config.App.Name, 
		config.Config.App.Environment)

	// StatsService 초기화
	if !config.IsFeatureEnabled("stats") {
		log.Println("⚠️ 통계 기능이 비활성화되어 있습니다")
		return
	}

	statsService, err := services.NewStatsService()
	if err != nil {
		log.Fatalf("❌ StatsService 초기화 실패: %v", err)
	}

	// 크론 스케줄러 시작
	scheduler, err := initializeCronScheduler(statsService)
	if err != nil {
		log.Fatalf("❌ 크론 스케줄러 초기화 실패: %v", err)
	}

	// 스케줄러 시작
	scheduler.Start()
	log.Println("✅ 크론잡 스케줄러 실행 중...")

	// 등록된 크론잡 목록 출력
	printCronJobs(scheduler)

	// 프로그램 종료 신호 대기
	waitForShutdown(scheduler)
}

// 크론 스케줄러 초기화
func initializeCronScheduler(statsService *services.StatsService) (*cron.Cron, error) {
	// 크론 스케줄러 생성 (한국 시간대)
	c := cron.New(cron.WithSeconds())

	// 매시간 통계 업데이트 (설정에서 가져오기)
	if schedule, exists := config.GetCronSchedule("hourly_stats"); exists {
		_, err := c.AddFunc(schedule, func() {
			log.Println("⏰ [매시간] 활성 대회 통계 업데이트 시작")
			if err := statsService.UpdateAllActiveCompetitions(); err != nil {
				log.Printf("❌ 활성 대회 통계 업데이트 실패: %v", err)
			} else {
				log.Println("✅ [매시간] 활성 대회 통계 업데이트 및 시간별 스냅샷 저장 완료")
			}
		})
		if err != nil {
			return nil, err
		}
		log.Printf("📅 매시간 통계 업데이트 스케줄 등록: %s", schedule)
	}

	// 일별 시스템 통계 업데이트
	if schedule, exists := config.GetCronSchedule("daily_stats"); exists {
		_, err := c.AddFunc(schedule, func() {
			log.Println("⏰ [매일] 전체 시스템 통계 업데이트 시작")
			if err := statsService.SaveDailyAggregation(); err != nil {
				log.Printf("❌ 시스템 통계 업데이트 실패: %v", err)
			} else {
				log.Println("✅ [매일] 전체 시스템 통계 업데이트 완료")
			}
		})
		if err != nil {
			log.Printf("⚠️ 일별 크론잡 등록 실패: %v", err)
		} else {
			log.Printf("📅 일별 시스템 통계 스케줄 등록: %s", schedule)
		}
	}

	// 주간 정리 작업 (설정이 있다면)
	if schedule, exists := config.GetCronSchedule("weekly_cleanup"); exists {
		_, err := c.AddFunc(schedule, func() {
			log.Println("⏰ [주간] 데이터 정리 작업 시작")
			// TODO: 오래된 로그 정리, 임시 파일 삭제 등
			log.Println("✅ [주간] 데이터 정리 작업 완료")
		})
		if err != nil {
			log.Printf("⚠️ 주간 정리 크론잡 등록 실패: %v", err)
		} else {
			log.Printf("📅 주간 정리 스케줄 등록: %s", schedule)
		}
	}

	return c, nil
}

// 등록된 크론잡 목록 출력
func printCronJobs(c *cron.Cron) {
	entries := c.Entries()
	if len(entries) == 0 {
		log.Println("⚠️ 등록된 크론잡이 없습니다")
		return
	}

	log.Printf("📋 등록된 크론잡 목록 (%d개):", len(entries))
	for i, entry := range entries {
		log.Printf("  %d. 다음 실행 시간: %v", i+1, entry.Next)
	}
}

// 종료 신호 대기
func waitForShutdown(scheduler *cron.Cron) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 종료 신호 받을 때까지 대기
	<-sigChan
	log.Println("🛑 크론잡 스케줄러 종료 중...")

	// 크론 정지
	scheduler.Stop()
	log.Println("✅ 크론잡 스케줄러 종료 완료")
}
