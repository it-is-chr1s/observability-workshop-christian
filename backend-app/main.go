package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
)

var (
	ctx = context.Background()
	rdb *redis.Client

	logger *slog.Logger
)

func generateHandler(w http.ResponseWriter, r *http.Request) {
	longURL, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Could not read request body", "error", err)

		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	shortLink := fmt.Sprintf("id%d", rand.Intn(10000))

	err = rdb.Set(ctx, shortLink, string(longURL), time.Hour*24).Err()
	if err != nil {
		logger.Error("Redis Set failed", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Info("Mapping created", "short_link", shortLink, "long_url", string(longURL))

	w.Write([]byte(shortLink))
}

func resolveHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	shortLink := vars["shortlink"]

	longURL, err := rdb.Get(ctx, shortLink).Result()
	if err == redis.Nil {

		logger.Warn("Link not found:", "short_link", shortLink)

		http.NotFound(w, r)
		return
	} else if err != nil {

		logger.Error("Redis Get failed", "error", err)

		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Write([]byte(longURL))
}

func main() {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{})
	logger = slog.New(jsonHandler).With("service", "backend-app")

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis-svc:6379"
	}

	rdb = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	logger.Info("Connecting with Redis", "redis_addr", redisAddr)
	//

	r := mux.NewRouter()
	r.HandleFunc("/generate", generateHandler).Methods("POST")
	r.HandleFunc("/resolve/{shortlink}", resolveHandler).Methods("GET")

	logger.Info("Backend-Service starting", "Port", 8081)

	http.ListenAndServe(":8081", r)
}
