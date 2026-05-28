package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func intFromString(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func nextID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func optionalRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func sanitizeID(input string) string {
	value := strings.TrimSpace(strings.ToLower(input))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == '-' || r == '_' || r == '.' || r == '@' {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func sanitizeSkillID(input string) string {
	return sanitizeID(input)
}

func queryValues(r *http.Request, key string) []string {
	if r == nil || r.URL == nil {
		return nil
	}
	values := r.URL.Query()[key]
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if text := strings.TrimSpace(part); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}
