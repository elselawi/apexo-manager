package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func jsonUnmarshal(raw string, v any) bool {
	return json.Unmarshal([]byte(raw), v) == nil
}

func nowString() string { return time.Now().UTC().Format(time.RFC3339) }

func parsePositiveInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
