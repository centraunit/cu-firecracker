/*
 * Firecracker CMS - HTTP Middleware
 * Copyright (c) 2025 CentraUnit Organization
 * All rights reserved.
 */

package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/centraunit/cu-firecracker-cms/internal/logger"
)

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware() mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap ResponseWriter to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			logger.WithRequest(r.Method, r.URL.String(), r.RemoteAddr).WithFields(logger.Fields{
				"status_code": wrapped.statusCode,
				"duration_ms": time.Since(start).Milliseconds(),
				"user_agent":  r.UserAgent(),
			}).Debug("HTTP request processed")
		})
	}
}

// RecoveryMiddleware recovers from panics and logs them
func RecoveryMiddleware() mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.WithRequest(r.Method, r.URL.String(), r.RemoteAddr).WithFields(logger.Fields{
						"panic": err,
					}).Error("Panic recovered in HTTP handler")

					http.Error(w, "Internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORSMiddleware adds CORS headers
func CORSMiddleware() mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ValidateSlugMiddleware validates plugin slug format
func ValidateSlugMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		slug := vars["slug"]

		if err := validateSlug(slug); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		next(w, r)
	}
}

// validateSlug validates plugin slug format
func validateSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("slug cannot be empty")
	}
	if len(slug) > 50 {
		return fmt.Errorf("slug too long (max 50 characters)")
	}
	if !isValidSlugFormat(slug) {
		return fmt.Errorf("slug contains invalid characters (use only letters, numbers, hyphens)")
	}
	return nil
}

// isValidSlugFormat checks if slug contains only valid characters
func isValidSlugFormat(slug string) bool {
	for _, char := range slug {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
			return false
		}
	}
	return true
}

// responseWriter wrapper to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
