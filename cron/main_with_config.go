package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
	
	"adfit-oauth/config"
	"adfit-oauth/services"
)

func main() {
	if err := config.LoadConfig("../config/app_config.yaml"); err != nil {
		log.Fatalf("❌ 설정 로드 실패: %v", err)
	}

	if !config.IsFeatureEnabled("cron") {
		log.Println("⚠️ Cron 기능이 비활성화되어 있습니다.")
		return
	}

	log.Printf("🚀 %s Cron 서버 시작 (환경: %s)", 
		config.Config.App.Name, 
		config.Config.App.Environment)

	statsService, err := services.NewStatsService()
	if err != nil {
		log.Fatalf("❌ StatsService 초기화 실패: %v", err)
		return
	}

	// ⭐ 서버 시작 시 한 번 실행 (5초 후)
	go func() {
		time.Sleep(5 * time.Second)
		// log.Println("🚀 [Cron 시작] 초기화...")
		
		statsService.CheckAndStartApprovedCompetitions()
		statsService.CheckAndFinishOngoingCompetitions()
		statsService.UpdateAllActiveCompetitions()
		
		// log.Println("✅ 초기화 완료")
	}()

	scheduler, err := initializeCronScheduler(statsService)
	if err != nil {
		log.Fatalf("❌ Cron 스케줄러 초기화 실패: %v", err)
	}

	scheduler.Start()
	log.Println("✅ Cron 스케줄러 시작 완료")

	printCronJobs(scheduler)
	waitForShutdown(scheduler)
}

func initializeCronScheduler(statsService *services.StatsService) (*cron.Cron, error) {
	c := cron.New(cron.WithSeconds())

	// 매 시간 정시 - 활성 대회 통계
	if schedule, exists := config.GetCronSchedule("hourly_stats"); exists {
		_, err := c.AddFunc(schedule, func() {
			// log.Println("⏰ [매 시간] 활성 대회 통계")
			statsService.UpdateAllActiveCompetitions()
		})
		if err != nil {
			return nil, err
		}
		log.Printf("📌 Hourly Stats 등록: %s", schedule)
	}

	// 매일 새벽 2시 - 시스템 통계
	if schedule, exists := config.GetCronSchedule("daily_stats"); exists {
		_, err := c.AddFunc(schedule, func() {
			// log.Println("⏰ [02:00] 시스템 통계")
			statsService.SaveDailyAggregation()
		})
		if err != nil {
			log.Printf("⚠️ Daily Stats 등록 실패: %v", err)
		} else {
			log.Printf("📌 Daily Stats 등록: %s", schedule)
		}
	}

	// 주간 정리
	if schedule, exists := config.GetCronSchedule("weekly_cleanup"); exists {
		_, err := c.AddFunc(schedule, func() {
			// log.Println("⏰ [주간] 정리 작업")
		})
		if err != nil {
			log.Printf("⚠️ Weekly Cleanup 등록 실패: %v", err)
		} else {
			log.Printf("📌 Weekly Cleanup 등록: %s", schedule)
		}
	}

	return c, nil
}

func printCronJobs(c *cron.Cron) {
	entries := c.Entries()
	if len(entries) == 0 {
		log.Println("⚠️ 등록된 Cron Job이 없습니다.")
		return
	}

	log.Printf("📋 등록된 Cron Job: %d개", len(entries))
	for i, entry := range entries {
		log.Printf("  %d. 다음 실행: %s", i+1, entry.Next.Format("2006-01-02 15:04:05"))
	}
}

func waitForShutdown(scheduler *cron.Cron) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("🛑 Cron 스케줄러 종료 중...")
	scheduler.Stop()
	log.Println("✅ Cron 스케줄러 종료 완료")
}
