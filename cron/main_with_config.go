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
	
	if err := config.LoadConfig("../config/app_config.yaml"); err != nil {
		log.Fatalf("???�정 로드 ?�패: %v", err)
	}

	
	if !config.IsFeatureEnabled("cron") {

		return
	}


		config.Config.App.Name, 
		config.Config.App.Environment)

	

		return
	}

	statsService, err := services.NewStatsService()
	if err != nil {
		log.Fatalf("??StatsService 초기???�패: %v", err)
	}

	
	scheduler, err := initializeCronScheduler(statsService)
	if err != nil {
		log.Fatalf("???�론 ?��?줄러 초기???�패: %v", err)
	}

	
	scheduler.Start()


	
	printCronJobs(scheduler)

	
}


	
	c := cron.New(cron.WithSeconds())

	
	if schedule, exists := config.GetCronSchedule("hourly_stats"); exists {
		_, err := c.AddFunc(schedule, func() {

			if err := statsService.UpdateAllActiveCompetitions(); err != nil {

			} else {

			}
		})
		if err != nil {
			return nil, err
		}

	}

	
	if schedule, exists := config.GetCronSchedule("daily_stats"); exists {
		_, err := c.AddFunc(schedule, func() {

			if err := statsService.SaveDailyAggregation(); err != nil {

			} else {

			}
		})
		if err != nil {

		} else {

		}
	}

	
	if schedule, exists := config.GetCronSchedule("weekly_cleanup"); exists {
		_, err := c.AddFunc(schedule, func() {

			
		})
		if err != nil {

		} else {

		}
	}

	return c, nil
}


func printCronJobs(c *cron.Cron) {
	entries := c.Entries()
	if len(entries) == 0 {

		return
	}


	for i, entry := range entries {

	}
}


	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	


	
	scheduler.Stop()

}
