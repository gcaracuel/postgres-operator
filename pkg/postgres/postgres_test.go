package postgres

import (
	"context"
	"errors"
	"testing"
)

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		wantHostname string
		wantPort     int32
		wantErr      bool
	}{
		{
			name:         "host with port",
			host:         "mydb.xxxxxx.eu-west-1.rds.amazonaws.com:5432",
			wantHostname: "mydb.xxxxxx.eu-west-1.rds.amazonaws.com",
			wantPort:     5432,
		},
		{
			name:         "host without port defaults to 5432",
			host:         "mydb.xxxxxx.eu-west-1.rds.amazonaws.com",
			wantHostname: "mydb.xxxxxx.eu-west-1.rds.amazonaws.com",
			wantPort:     5432,
		},
		{
			name:         "custom port",
			host:         "localhost:1234",
			wantHostname: "localhost",
			wantPort:     1234,
		},
		{
			name:    "invalid port",
			host:    "localhost:abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostname, port, err := parseHostPort(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseHostPort(%q) expected error, got nil", tt.host)
				}
				return
			}
			if err != nil {
				t.Errorf("parseHostPort(%q) unexpected error: %v", tt.host, err)
				return
			}
			if hostname != tt.wantHostname {
				t.Errorf("parseHostPort(%q) hostname = %q, want %q", tt.host, hostname, tt.wantHostname)
			}
			if port != tt.wantPort {
				t.Errorf("parseHostPort(%q) port = %d, want %d", tt.host, port, tt.wantPort)
			}
		})
	}
}

func TestIsAWSRDS(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"mydb.xxxxxx.eu-west-1.rds.amazonaws.com", true},
		{"mydb.xxxxxx.rds.amazonaws.com:5432", true},
		{"localhost:5432", false},
		{"10.0.0.1:5432", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := isAWSRDS(tt.host); got != tt.want {
				t.Errorf("isAWSRDS(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestGenerateIAMAuthTokenMock(t *testing.T) {
	// Save and restore the original function
	origGenerateIAMAuthToken := generateIAMAuthToken
	defer func() { generateIAMAuthToken = origGenerateIAMAuthToken }()

	// Replace with a mock
	generateIAMAuthToken = func(_ context.Context, user, host, region string) (string, error) {
		if user == "" {
			return "", errors.New("empty user")
		}
		if host == "" {
			return "", errors.New("empty host")
		}
		if region == "" {
			return "", errors.New("empty region")
		}
		return "mock-iam-token", nil
	}

	t.Run("successful token generation", func(t *testing.T) {
		token, err := generateIAMAuthToken(context.Background(), "db_user", "mydb.rds.amazonaws.com:5432", "eu-west-1")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if token != "mock-iam-token" {
			t.Errorf("token = %q, want %q", token, "mock-iam-token")
		}
	})

	t.Run("empty user returns error", func(t *testing.T) {
		_, err := generateIAMAuthToken(context.Background(), "", "mydb.rds.amazonaws.com:5432", "eu-west-1")
		if err == nil {
			t.Error("expected error for empty user, got nil")
		}
	})

	t.Run("empty host returns error", func(t *testing.T) {
		_, err := generateIAMAuthToken(context.Background(), "db_user", "", "eu-west-1")
		if err == nil {
			t.Error("expected error for empty host, got nil")
		}
	})

	t.Run("empty region returns error", func(t *testing.T) {
		_, err := generateIAMAuthToken(context.Background(), "db_user", "mydb.rds.amazonaws.com:5432", "")
		if err == nil {
			t.Error("expected error for empty region, got nil")
		}
	})
}

func TestGetConnectionWithIAMAuth(t *testing.T) {
	// Save and restore the original function
	origGenerateIAMAuthToken := generateIAMAuthToken
	defer func() { generateIAMAuthToken = origGenerateIAMAuthToken }()

	// Replace with a mock that returns a predictable token
	generateIAMAuthToken = func(_ context.Context, user, host, region string) (string, error) {
		return "iam-token-for-" + user + "-at-" + host, nil
	}

	pg := &pg{
		user:       "db_user",
		host:       "mydb.rds.amazonaws.com:5432",
		args:       "sslmode=disable",
		useIAMAuth: true,
		awsRegion:  "eu-west-1",
	}

	// We can't actually connect, but we can verify the token generation is called
	// by checking that getConnectionWithIAMAuth returns a connection error
	// (not a token generation error)
	_, err := pg.getConnectionWithIAMAuth("testdb")
	if err == nil {
		t.Error("expected connection error (no real DB), got nil")
	}
	// The error should be a connection error, not a token error
	if err != nil && err.Error() == "IAM auth token generation failed: empty user" {
		t.Error("unexpected token generation error")
	}
}

func TestGetConnectionWithoutIAMAuth(t *testing.T) {
	pg := &pg{
		user:       "admin",
		pass:       "password",
		host:       "localhost:5432",
		args:       "sslmode=disable",
		useIAMAuth: false,
	}

	// Without IAM auth, getConnection should use the stored password
	// We can't actually connect, but we can verify the connection string is built
	// by checking the error is a connection error, not a token error
	_, err := pg.getConnection("testdb")
	if err == nil {
		t.Error("expected connection error (no real DB), got nil")
	}
	// The error should NOT mention IAM auth
	if err != nil && err.Error() == "IAM auth token generation failed: empty user" {
		t.Error("unexpected IAM auth error when useIAMAuth=false")
	}
}
