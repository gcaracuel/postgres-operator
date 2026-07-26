package config

import (
	"os"
	"sync"
	"testing"
)

func TestGetWithIAMAuth(t *testing.T) {
	// Reset the singleton so Get() re-reads env vars
	config = nil
	doOnce = sync.Once{}

	// Set env vars for IAM auth
	os.Setenv("POSTGRES_HOST", "mydb.rds.amazonaws.com:5432")
	os.Setenv("POSTGRES_USER", "db_user")
	os.Setenv("POSTGRES_PASS", "ignored")
	os.Setenv("POSTGRES_URI_ARGS", "sslmode=disable")
	os.Setenv("POSTGRES_CLOUD_PROVIDER", "AWS")
	os.Setenv("POSTGRES_USE_IAM_AUTH", "true")
	os.Setenv("AWS_REGION", "eu-west-1")
	os.Setenv("POSTGRES_DEFAULT_DATABASE", "postgres")

	defer func() {
		os.Unsetenv("POSTGRES_HOST")
		os.Unsetenv("POSTGRES_USER")
		os.Unsetenv("POSTGRES_PASS")
		os.Unsetenv("POSTGRES_URI_ARGS")
		os.Unsetenv("POSTGRES_CLOUD_PROVIDER")
		os.Unsetenv("POSTGRES_USE_IAM_AUTH")
		os.Unsetenv("AWS_REGION")
		os.Unsetenv("POSTGRES_DEFAULT_DATABASE")
		// Reset for other tests
		config = nil
		doOnce = sync.Once{}
	}()

	cfg := Get()

	if !cfg.PostgresUseIAMAuth {
		t.Error("PostgresUseIAMAuth should be true")
	}
	if cfg.AwsRegion != "eu-west-1" {
		t.Errorf("AwsRegion = %q, want %q", cfg.AwsRegion, "eu-west-1")
	}
	if cfg.CloudProvider != CloudProviderAWS {
		t.Errorf("CloudProvider = %q, want %q", cfg.CloudProvider, CloudProviderAWS)
	}
}

func TestGetWithoutIAMAuth(t *testing.T) {
	// Reset the singleton
	config = nil
	doOnce = sync.Once{}

	os.Setenv("POSTGRES_HOST", "localhost:5432")
	os.Setenv("POSTGRES_USER", "admin")
	os.Setenv("POSTGRES_PASS", "password")
	os.Setenv("POSTGRES_URI_ARGS", "sslmode=disable")
	os.Setenv("POSTGRES_DEFAULT_DATABASE", "postgres")

	defer func() {
		os.Unsetenv("POSTGRES_HOST")
		os.Unsetenv("POSTGRES_USER")
		os.Unsetenv("POSTGRES_PASS")
		os.Unsetenv("POSTGRES_URI_ARGS")
		os.Unsetenv("POSTGRES_DEFAULT_DATABASE")
		config = nil
		doOnce = sync.Once{}
	}()

	cfg := Get()

	if cfg.PostgresUseIAMAuth {
		t.Error("PostgresUseIAMAuth should be false when not set")
	}
	if cfg.AwsRegion != "" {
		t.Errorf("AwsRegion should be empty, got %q", cfg.AwsRegion)
	}
}

func TestParseCloudProvider(t *testing.T) {
	tests := []struct {
		input string
		want  CloudProvider
	}{
		{"aws", CloudProviderAWS},
		{"AWS", CloudProviderAWS},
		{"Aws", CloudProviderAWS},
		{"azure", CloudProviderAzure},
		{"gcp", CloudProviderGCP},
		{"", CloudProviderNone},
		{"unknown", CloudProviderNone},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseCloudProvider(tt.input); got != tt.want {
				t.Errorf("ParseCloudProvider(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
