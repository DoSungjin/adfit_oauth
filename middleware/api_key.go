package middleware

import (
	"net/http"
	"os"
	"strings"
	
	"github.com/gin-gonic/gin"
)


func APIKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		
		if c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		
		
		if strings.Contains(c.Request.URL.Path, "/callback") {
			c.Next()
			return
		}
		
		
		apiKey := c.GetHeader("X-API-Key")
		expectedKey := os.Getenv("API_KEY")
		
		
		if expectedKey == "" && os.Getenv("ENVIRONMENT") != "production" {
			c.Next()
			return
		}
		
		if apiKey != expectedKey {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid or missing API key",
			})
			c.Abort()
			return
		}
		
		c.Next()
	}
}


func RequireAPIKey(paths ...string) gin.HandlerFunc {
	pathMap := make(map[string]bool)
	for _, path := range paths {
		pathMap[path] = true
	}
	
	return func(c *gin.Context) {
		
		if !pathMap[c.Request.URL.Path] {
			c.Next()
			return
		}
		
		
	}
}
