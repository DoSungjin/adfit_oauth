package handler

import (
    "fmt"
    "net/http"
)

func Handler(w http.ResponseWriter, r *http.Request) {
    // CORS 설정
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
    
    if r.Method == "OPTIONS" {
        w.WriteHeader(http.StatusOK)
        return
    }
    
    code := r.URL.Query().Get("code")
    state := r.URL.Query().Get("state")
    
    // Flutter 앱으로 리다이렉트
    clientRedirect := fmt.Sprintf(
        "https://adfit.ai/auth/callback/tiktok?code=%s&state=%s",
        code,
        state,
    )
    
    http.Redirect(w, r, clientRedirect, http.StatusTemporaryRedirect)
}
