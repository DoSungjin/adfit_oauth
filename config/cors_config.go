package config

import (
	"os"
)


func GetAllowedOrigins() []string {
	env := os.Getenv("ENVIRONMENT")

	if env == "production" {
		return []string{
			"https://adtown.ai",
			"https://www.adtown.ai",
			"https://posted-app-c4ff5.web.app",
			"https://posted-app-c4ff5.firebaseapp.com",
		}
	}

	
	return []string{
		"http://localhost:9000",
		"http://localhost:3000",
		"http://127.0.0.1:9000",
		"https://adtown.ai",
		"https://posted-app-c4ff5.web.app",
	}
}
