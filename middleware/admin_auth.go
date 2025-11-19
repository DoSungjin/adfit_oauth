package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminAuthRequired - Admin 인증 미들웨어
func AdminAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Authorization 헤더 확인
		authHeader := c.GetHeader("Authorization")
		adminToken := c.GetHeader("X-Admin-Token")

		// 간단한 토큰 검증 (실제로는 더 복잡한 검증 필요)
		validAuthToken := "Bearer adfit-stats-update-token"
		validAdminToken := "adfit-admin-secret"

		if authHeader != validAuthToken || adminToken != validAdminToken {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: Invalid admin credentials",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
