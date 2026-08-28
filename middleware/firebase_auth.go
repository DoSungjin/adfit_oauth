package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"

	"adfit-oauth/config"
)

var firebaseAuth *auth.Client

func InitFirebaseAuth() error {
	ctx := context.Background()

	var app *firebase.App
	var err error

	if config.Config != nil {
		if config.Config.Firebase.CredentialsPath != "" {
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID: config.Config.Firebase.ProjectID,
			}, option.WithCredentialsFile(config.Config.Firebase.CredentialsPath))
		} else {
			// 자격증명 없는 환경(AWS EC2): ID 토큰 검증은 구글 공개 인증서만 쓰므로
			// WithoutAuthentication 으로 초기화한다. VerifyIDToken 은 정상 동작하고,
			// 유저 관리 API(CreateUser 등)만 불가 — 이 미들웨어는 검증 전용이라 무관.
			app, err = firebase.NewApp(ctx, &firebase.Config{
				ProjectID: config.Config.Firebase.ProjectID,
			}, option.WithoutAuthentication())
		}
	} else {
		app, err = firebase.NewApp(ctx, &firebase.Config{
			ProjectID: "posted-app-c4ff5",
		}, option.WithoutAuthentication())
	}

	if err != nil {
		return fmt.Errorf("firebase 초기???�패: %v", err)
	}

	firebaseAuth, err = app.Auth(ctx)
	if err != nil {
		return fmt.Errorf("firebase auth 초기???�패: %v", err)
	}

	return nil
}

func FirebaseAuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(401, gin.H{
				"error": "Authorization header required",
				"code":  "NO_AUTH_HEADER",
			})
			c.Abort()
			return
		}

		
		idToken := strings.TrimPrefix(authHeader, "Bearer ")
		if idToken == authHeader {
			c.JSON(401, gin.H{
				"error": "Invalid authorization format. Use 'Bearer <token>'",
				"code":  "INVALID_AUTH_FORMAT",
			})
			c.Abort()
			return
		}

		// Firebase ID Token 검증
		ctx := context.Background()
		token, err := firebaseAuth.VerifyIDToken(ctx, idToken)
		if err != nil {
			c.JSON(401, gin.H{
				"error": "Invalid or expired Firebase ID token",
				"code":  "INVALID_TOKEN",
			})
			c.Abort()
			return
		}

		// ⭐ Context에 user_id 저장 (핵심!)
		c.Set("firebase_token", token)
		c.Set("user_id", token.UID)

		c.Next()
	}
}
