package handler

import (
    "fmt"
    "net/http"
    "net/url"
    "os"
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
    
    clientKey := os.Getenv("TIKTOK_CLIENT_KEY")
    if clientKey == "" {
        clientKey = "sbaw680qp988gxobwf"
    }
    
    redirectURI := "https://adfit-oauth.vercel.app/api/tiktok/callback"
    
    state := r.URL.Query().Get("state")
    if state == "" {
        state = "random_state_123"
    }
    
    authURL := fmt.Sprintf(
        "https://www.tiktok.com/auth/authorize?client_key=%s&redirect_uri=%s&state=%s&response_type=code&scope=user.info.basic,video.list",
        clientKey,
        url.QueryEscape(redirectURI),
        state,
    )
    
    http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}
