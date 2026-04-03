package metrics

import (
	"net/http"
	"time"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter

	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// ChiMiddleware returns a chi-compatible middleware that records
// api_response_ms for every request.
func ChiMiddleware(c *Collector) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}

			next.ServeHTTP(rec, r)

			c.RecordAPIResponse(time.Since(start), r.Method, r.URL.Path, rec.code)
		})
	}
}
