package auth

import (
	"strings"
	"testing"
)

func TestRedactString_Secrets(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bootstrap Token",
			input:    "Worker auth token is clx-btk-18d93b90fa37991c-4fe526490772 in cluster",
			expected: "Worker auth token is clx-btk-[REDACTED] in cluster",
		},
		{
			name:     "Bearer Authorization Header",
			input:    "Headers: Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.t-IDx_7",
			expected: "Headers: Authorization: Bearer [REDACTED]",
		},
		{
			name:     "URI with embedded password",
			input:    "Connecting to postgres://admin:SuperSecretPass123!@db.internal:5432/cloudx",
			expected: "Connecting to postgres://admin:[REDACTED]@db.internal:5432/cloudx",
		},
		{
			name:     "PEM Private Key",
			input:    "Server key:\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0r...\n-----END RSA PRIVATE KEY-----\nEnd of cert",
			expected: "Server key:\n[REDACTED_PRIVATE_KEY]\nEnd of cert",
		},
		{
			name:     "Key-value secret assignments",
			input:    "config: password=my_db_password, api_key='sk-1234567890abcdef'",
			expected: "config: password=[REDACTED], api_key=[REDACTED]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.input)
			if got != tc.expected {
				t.Errorf("RedactString mismatch:\nGot:      %q\nExpected: %q", got, tc.expected)
			}
		})
	}
}

func TestRedactEnvironmentVariables(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "postgres://user:secret@localhost:5432/db",
		"DB_PASSWORD":  "MySecret123",
		"API_KEY":      "sk_live_123456",
		"SERVICE_PORT": "8080",
		"LOG_LEVEL":    "info",
		"APP_NAME":     "payment-service",
		"AUTH_TOKEN":   "token-xyz-123",
		"TLS_KEY_FILE": "/etc/tls/key.pem",
	}

	redacted := RedactEnvironmentVariables(env)

	if redacted["DATABASE_URL"] != "[REDACTED]" {
		t.Errorf("expected DATABASE_URL to be [REDACTED], got: %s", redacted["DATABASE_URL"])
	}
	if redacted["DB_PASSWORD"] != "[REDACTED]" {
		t.Errorf("expected DB_PASSWORD to be [REDACTED], got: %s", redacted["DB_PASSWORD"])
	}
	if redacted["API_KEY"] != "[REDACTED]" {
		t.Errorf("expected API_KEY to be [REDACTED], got: %s", redacted["API_KEY"])
	}
	if redacted["AUTH_TOKEN"] != "[REDACTED]" {
		t.Errorf("expected AUTH_TOKEN to be [REDACTED], got: %s", redacted["AUTH_TOKEN"])
	}
	if redacted["TLS_KEY_FILE"] != "[REDACTED]" {
		t.Errorf("expected TLS_KEY_FILE to be [REDACTED], got: %s", redacted["TLS_KEY_FILE"])
	}

	// Non-sensitive variables must remain intact
	if redacted["SERVICE_PORT"] != "8080" {
		t.Errorf("expected SERVICE_PORT to remain '8080', got: %s", redacted["SERVICE_PORT"])
	}
	if redacted["LOG_LEVEL"] != "info" {
		t.Errorf("expected LOG_LEVEL to remain 'info', got: %s", redacted["LOG_LEVEL"])
	}
	if redacted["APP_NAME"] != "payment-service" {
		t.Errorf("expected APP_NAME to remain 'payment-service', got: %s", redacted["APP_NAME"])
	}
}

func TestRedactCommandArgs(t *testing.T) {
	args := []string{
		"server",
		"--port", "8080",
		"--password", "MyP@ssw0rd!",
		"--token=clx-btk-secret-123",
		"--api-key", "secret-key-xyz",
		"--config", "/etc/cloudx.yaml",
	}

	redacted := RedactCommandArgs(args)
	joined := strings.Join(redacted, " ")

	if strings.Contains(joined, "MyP@ssw0rd!") {
		t.Errorf("password leaked in command args: %s", joined)
	}
	if strings.Contains(joined, "clx-btk-secret-123") {
		t.Errorf("token leaked in command args: %s", joined)
	}
	if strings.Contains(joined, "secret-key-xyz") {
		t.Errorf("api-key leaked in command args: %s", joined)
	}
	if !strings.Contains(joined, "--port 8080") {
		t.Errorf("expected non-sensitive args to remain intact: %s", joined)
	}
}

func TestRedactJSONPayload(t *testing.T) {
	rawJSON := `{"service":"auth","db_password":"secretPassword123","token":"clx-btk-abcdef","status":"READY","port":8080}`
	redactedJSON := RedactJSONPayload(rawJSON)

	if strings.Contains(redactedJSON, "secretPassword123") {
		t.Errorf("db_password leaked in JSON: %s", redactedJSON)
	}
	if strings.Contains(redactedJSON, "clx-btk-abcdef") {
		t.Errorf("token leaked in JSON: %s", redactedJSON)
	}
	if !strings.Contains(redactedJSON, "auth") || !strings.Contains(redactedJSON, "READY") {
		t.Errorf("expected normal values to be preserved: %s", redactedJSON)
	}
}
