package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
	
	"adfit-oauth/services"
)

func main() {
	
	if err := godotenv.Load(); err != nil {
// 		log.Println("No .env file found")
	}



	
	if err != nil {
		log.Fatalf("??StatsService 초기???�패: %v", err)
	}

	
	kst := time.FixedZone("KST", 9*60*60)
	c := cron.New(cron.WithLocation(kst), cron.WithSeconds())

	
	
	
	_, err = c.AddFunc("0 0 0 * * *", func() {

		
		
		if err := statsService.CheckAndStartApprovedCompetitions(); err != nil {

		}
		
		
		if err := statsService.CheckAndFinishOngoingCompetitions(); err != nil {

		}
	})
	if err != nil {
		log.Fatalf("???�정 ?�론???�록 ?�패: %v", err)
	}

	
	_, err = c.AddFunc("0 0 1 * * *", func() {

		
		
		if err := statsService.CheckAndStartApprovedCompetitions(); err != nil {

		}
		
		
		if err := statsService.CheckAndFinishOngoingCompetitions(); err != nil {

		}
	})
	if err != nil {
		log.Fatalf("???�전 1???�론???�록 ?�패: %v", err)
	}

	
	_, err = c.AddFunc("0 0 2 * * *", func() {

		
		

		}
		
		

		if err := statsService.UpdateDailySystemStats(); err != nil {

		}
	})
	if err != nil {

	}

	
	
	
	/*
	_, err = c.AddFunc("0 0 * * * *", func() {

		if err := statsService.UpdateAllActiveCompetitions(); err != nil {

		}
	})
	if err != nil {

	}
	*/

	
	c.Start()



	
	entries := c.Entries()

	for i, entry := range entries {

	}

	
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	


	
	c.Stop()

}
