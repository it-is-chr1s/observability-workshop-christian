package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	backendServiceURL string

	// Define metric variables globally
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec

	req *prometheus.Registry
)

func init() {
	log.Printf("INFO: Initializing metrics")

	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"method", "path", "code"})

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "HTTP request duration in seconds.",
		},
		[]string{"method", "path"})

	log.Println("INFO: Registering metrics...")
	req = prometheus.NewRegistry()
	req.MustRegister(httpRequestsTotal)
	req.MustRegister(httpRequestDuration)
	log.Println("INFO: Metrics successfully registered.")
}

// responseWriter is a wrapper for http.ResponseWriter,
// allowing us to capture the status code.
// (This struct is provided for you. No changes needed.)
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, http.StatusOK}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// prometheusMiddleware is our middleware that instruments every request.
func prometheusMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := newResponseWriter(w)

		// Call the next handler in the chain
		next.ServeHTTP(rw, r)

		// Record metrics after the request has been handled
		duration := time.Since(start).Seconds()

		// Get the route (e.g., "/shorten" or "/{shortlink}")
		route := mux.CurrentRoute(r)
		path, _ := route.GetPathTemplate()
		if path == "" {
			path = "unknown"
		}
		method := r.Method
		if method == "" {
			method = "GET"
		}

		statusCodeStr := strconv.Itoa(rw.statusCode)

		httpRequestDuration.With(prometheus.Labels{"method": method, "path": path}).Observe(duration)
		httpRequestsTotal.With(prometheus.Labels{"method": method, "path": path, "code": statusCodeStr}).Inc()

	})
}

func shortenHandler(w http.ResponseWriter, r *http.Request) {
	longURL, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("ERROR: couldn't read request body: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	resp, err := http.Post(backendServiceURL+"/generate", "text/plain", bytes.NewReader(longURL))
	if err != nil {
		log.Printf("ERROR: Backend connection failed: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	shortLink, _ := io.ReadAll(resp.Body)
	log.Printf("INFO: Link shortened: %s -> %s", string(longURL), string(shortLink))
	w.Write(append(shortLink, '\n'))
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	shortLink := vars["shortlink"]

	resp, err := http.Get(backendServiceURL + "/resolve/" + shortLink)
	if err != nil {
		log.Printf("ERROR: Backend connection failed: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if resp.StatusCode == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}

	longURL, _ := io.ReadAll(resp.Body)
	log.Printf("INFO: Redirect: %s -> %s", shortLink, string(longURL))
	http.Redirect(w, r, string(longURL), http.StatusFound)
}

func main() {
	backendServiceURL = os.Getenv("BACKEND_SVC_URL")
	if backendServiceURL == "" {
		backendServiceURL = "http://backend-app-svc:8081"
	}
	log.Printf("INFO: Backend-Service URL on: %s", backendServiceURL)

	r := mux.NewRouter()
	r.HandleFunc("/shorten", shortenHandler).Methods("POST")
	r.HandleFunc("/{shortlink}", redirectHandler).Methods("GET")
	r.Use(prometheusMiddleware)

	// Start the /metrics server on port 9090
	// (This part is provided for you. No changes needed.)
	go func() {
		metricsRouter := mux.NewRouter()
		metricsRouter.Handle("/metrics", promhttp.HandlerFor(req, promhttp.HandlerOpts{}))
		log.Println("INFO: Metrics server started on Port 9090")
		if err := http.ListenAndServe(":9090", metricsRouter); err != nil {
			log.Fatalf("FATAL: Couldn't start metrics server: %v", err)
		}
	}()

	log.Println("INFO: Frontend-Service starting on Port 8080")
	http.ListenAndServe(":8080", r)
}
