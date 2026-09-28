package auth

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	// Redaction patterns for sensitive credentials, bootstrap tokens, authorization tokens, and private keys
	bootstrapTokenRegex = regexp.MustCompile(`clx-btk-[a-zA-Z0-9_-]+`)
	bearerTokenRegex    = regexp.MustCompile(`(?i)(bearer\s+)[a-zA-Z0-9_\-\.]+`)
	pemPrivateKeyRegex  = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	secretKeyValueRegex = regexp.MustCompile(`(?i)\b(password|secret|token|api_key|apikey|private_key|jwt|database_url|db_password)\s*[:=]\s*["']?([^"'\s,;]+)["']?`)
	jwtRegex            = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]+\.eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\b`)
	uriPasswordRegex    = regexp.MustCompile(`(?i)([a-zA-Z][a-zA-Z0-9+.-]*://[^:]+:)([^@]+)(@)`)
	awsAccessKeyRegex   = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}:[a-zA-Z0-9/+=]{40}\b`)
)

// RedactString scans any text and redacts credentials, bootstrap tokens, JWTs, URI passwords, AWS keys, and private keys.
func RedactString(input string) string {
	if input == "" {
		return input
	}
	out := pemPrivateKeyRegex.ReplaceAllString(input, "[REDACTED_PRIVATE_KEY]")
	out = bootstrapTokenRegex.ReplaceAllString(out, "clx-btk-[REDACTED]")
	out = bearerTokenRegex.ReplaceAllString(out, "${1}[REDACTED]")
	out = jwtRegex.ReplaceAllString(out, "[REDACTED_JWT]")
	out = uriPasswordRegex.ReplaceAllString(out, "${1}[REDACTED]${3}")
	out = awsAccessKeyRegex.ReplaceAllString(out, "[REDACTED_AWS_KEY]")
	out = secretKeyValueRegex.ReplaceAllString(out, "${1}=[REDACTED]")
	return out
}

// IsSensitiveKey returns true if a key name suggests sensitive credential data.
func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	lower = strings.TrimLeft(lower, "-")
	lower = strings.ReplaceAll(lower, "-", "_")
	if lower == "" {
		return false
	}
	sensitiveSubstrings := []string{
		"password",
		"passwd",
		"secret",
		"token",
		"api_key",
		"apikey",
		"private_key",
		"privkey",
		"auth",
		"credential",
		"cert_key",
		"key_file",
		"jwt",
		"db_pass",
		"database_url",
	}
	for _, s := range sensitiveSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// RedactValue redacts a single value based on its key and content.
func RedactValue(key string, val any) any {
	if IsSensitiveKey(key) {
		return "[REDACTED]"
	}

	switch v := val.(type) {
	case string:
		return RedactString(v)
	case map[string]string:
		return RedactMap(v)
	case map[string]any:
		return RedactMapAny(v)
	case []string:
		redactedList := make([]string, len(v))
		for i, item := range v {
			redactedList[i] = RedactString(item)
		}
		return redactedList
	case []any:
		redactedList := make([]any, len(v))
		for i, item := range v {
			redactedList[i] = RedactValue(key, item)
		}
		return redactedList
	default:
		return val
	}
}

// RedactMap redacts sensitive keys and values in a map[string]string.
func RedactMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if IsSensitiveKey(k) {
			out[k] = "[REDACTED]"
		} else {
			out[k] = RedactString(v)
		}
	}
	return out
}

// RedactMapAny redacts sensitive keys and values in a map[string]any.
func RedactMapAny(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = RedactValue(k, v)
	}
	return out
}

// RedactJSONPayload parses a JSON string, sanitizes sensitive keys and secret patterns, and re-encodes it.
// If the payload is not valid JSON, it falls back to RedactString.
func RedactJSONPayload(payload string) string {
	if strings.TrimSpace(payload) == "" {
		return payload
	}
	var obj any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return RedactString(payload)
	}

	switch v := obj.(type) {
	case map[string]any:
		sanitized := RedactMapAny(v)
		bytes, err := json.Marshal(sanitized)
		if err == nil {
			return string(bytes)
		}
	case []any:
		sanitized := RedactValue("", v)
		bytes, err := json.Marshal(sanitized)
		if err == nil {
			return string(bytes)
		}
	}

	return RedactString(payload)
}

// RedactEnvironmentVariables redacts sensitive environment key-value pairs (e.g. for inspection / CLI output).
func RedactEnvironmentVariables(env map[string]string) map[string]string {
	return RedactMap(env)
}

// RedactCommandArgs inspects command arguments and masks sensitive flags like --token=XYZ or --password XYZ.
func RedactCommandArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	redactNext := false
	for i, arg := range args {
		if redactNext {
			out[i] = "[REDACTED]"
			redactNext = false
			continue
		}

		lower := strings.ToLower(arg)
		// Check for flags with inline values: --password=XYZ, --token=XYZ, -p=XYZ
		if strings.HasPrefix(arg, "-") && strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if IsSensitiveKey(parts[0]) {
				out[i] = fmt.Sprintf("%s=[REDACTED]", parts[0])
				continue
			}
		}

		// Check for flags where next argument is the value: --password XYZ, --token XYZ
		if strings.HasPrefix(arg, "-") && IsSensitiveKey(lower) {
			out[i] = arg
			redactNext = true
			continue
		}

		out[i] = RedactString(arg)
	}
	return out
}
