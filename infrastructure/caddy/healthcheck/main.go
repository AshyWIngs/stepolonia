package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	// Проверяем реальный приватный HTTP listener, а не только синтаксис Caddyfile.
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:8081/health/live")
	if err != nil {
		os.Exit(1)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		os.Exit(1)
	}
}
