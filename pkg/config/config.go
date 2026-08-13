package config

import (
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/movetokube/postgres-operator/pkg/utils"
)

type Cfg struct {
	PostgresHost       string
	PostgresUser       string
	PostgresPass       string
	PostgresUriArgs    string
	PostgresDefaultDb  string
	CloudProvider      CloudProvider
	AnnotationFilter   string
	KeepSecretName     bool
	PostgresUseIAMAuth bool
	AwsRegion          string
}

var (
	doOnce sync.Once
	config *Cfg
)

type CloudProvider string

const (
	CloudProviderNone  CloudProvider = "None"
	CloudProviderAWS   CloudProvider = "AWS"
	CloudProviderAzure CloudProvider = "Azure"
	CloudProviderGCP   CloudProvider = "GCP"
)

func Get() *Cfg {
	doOnce.Do(func() {
		config = &Cfg{}
		config.PostgresHost = utils.MustGetEnv("POSTGRES_HOST")
		config.PostgresUser = url.PathEscape(utils.MustGetEnv("POSTGRES_USER"))
		config.PostgresUriArgs = utils.GetEnv("POSTGRES_URI_ARGS")
		config.PostgresDefaultDb = utils.GetEnv("POSTGRES_DEFAULT_DATABASE")
		config.CloudProvider = ParseCloudProvider(utils.GetEnv("POSTGRES_CLOUD_PROVIDER"))
		config.PostgresUseIAMAuth, _ = strconv.ParseBool(utils.GetEnv("POSTGRES_USE_IAM_AUTH"))
		config.AwsRegion = utils.GetEnv("AWS_REGION")

		if config.PostgresUseIAMAuth {
			// POSTGRES_PASS is not needed when using IAM database authentication.
			// The operator generates a short-lived IAM auth token instead.
			config.PostgresPass = ""
		} else {
			config.PostgresPass = url.PathEscape(utils.MustGetEnv("POSTGRES_PASS"))
		}
		config.AnnotationFilter = utils.GetEnv("POSTGRES_INSTANCE")
		if value, err := strconv.ParseBool(utils.GetEnv("KEEP_SECRET_NAME")); err == nil {
			config.KeepSecretName = value
		}
	})
	return config
}

// CloudProvider is an enum for supported cloud providers.

func ParseCloudProvider(s string) CloudProvider {
	switch strings.ToLower(s) {
	case "aws":
		return CloudProviderAWS
	case "azure":
		return CloudProviderAzure
	case "gcp":
		return CloudProviderGCP
	default:
		return CloudProviderNone
	}
}
