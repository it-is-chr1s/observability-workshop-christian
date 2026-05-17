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
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	ctx    = context.Background()
	rdb    *redis.Client
	logger *slog.Logger
	tracer trace.Tracer
)

func init() {

	logger = slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "backend-app")

	if _, err := initTracerProvider(logger); err != nil {
		logger.Error("Failed to initialize OTel TracerProvider", "error", err)
	}
	tracer = otel.Tracer("backend-app")
}

func generateHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, span := tracer.Start(ctx, "generateHandler")
	defer span.End()

	longURL, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	shortLink := fmt.Sprintf("id%d", rand.Intn(10000))

	redisCtx, redisSpan := tracer.Start(ctx, "redis-set")
	redisSpan.SetAttributes(
		attribute.String("db.system", "redis"),
		attribute.String("db.key", shortLink),
	)

	err = rdb.Set(redisCtx, shortLink, string(longURL), time.Hour*24).Err()

	redisSpan.End()

	if err != nil {
		logger.Error("Redis SET failed", "error", err, "short_link", shortLink)
		http.Error(w, "Internal error in backend-app", http.StatusInternalServerError)
		return
	}

	logger.Info("Mapping created", "short_link", shortLink, "long_url", string(longURL))
	w.Write([]byte(shortLink))
}

func resolveHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, span := tracer.Start(ctx, "resolveHandler")
	defer span.End()

	vars := mux.Vars(r)
	shortLink := vars["shortlink"]

	redisCtx, redisSpan := tracer.Start(ctx, "redis-get")
	redisSpan.SetAttributes(
		attribute.String("db.system", "redis"),
		attribute.String("db.key", shortLink),
	)

	longURL, err := rdb.Get(redisCtx, shortLink).Result()

	redisSpan.End()

	if err == redis.Nil {
		logger.Warn("Link not found", "short_link", shortLink)
		http.NotFound(w, r)
		return
	} else if err != nil {
		logger.Error("Redis GET failed", "error", err, "short_link", shortLink)
		http.Error(w, "Internal error in backend-app", http.StatusInternalServerError)
		return
	}

	w.Write([]byte(longURL))
}

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis-svc:6379"
	}

	rdb = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	logger.Info("Connecting to Redis", "address", redisAddr)

	r := mux.NewRouter()
	r.HandleFunc("/generate", generateHandler).Methods("POST")
	r.HandleFunc("/resolve/{shortlink}", resolveHandler).Methods("GET")

	r.Use(otelmux.Middleware("backend-router"))

	logger.Info("Backend service starting", "port", 8081)

	if err := http.ListenAndServe(":8081", r); err != nil {
		logger.Error("Backend server failed to start", "error", err)
	}
}
