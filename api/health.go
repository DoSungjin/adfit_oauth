package handler

import (
    "encoding/json"
    "fmt"
    "net/http"
    "os"
)

func Handler(w http.ResponseWriter, r *http.Request) {
    // CORS 설정
    w.Header().Set("Access-Control-Allow-Origin", "*")
    w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
    w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
    
    if r.Method == "OPTIONS" {
        w.WriteHeader(http.StatusOK)
        return
    }
    
    // Health check
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{
        "status": "ok",
        "path": r.URL.Path,
        "method": r.Method,
    })
}
