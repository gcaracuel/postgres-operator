package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	rdsauth "github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/go-logr/logr"
	"github.com/movetokube/postgres-operator/pkg/config"
)

// PG is the interface for PostgreSQL operations.
type PG interface {
	CreateDB(dbname, username string) error
	CreateSchema(db, role, schema string) error
	CreateExtension(db, extension string) error
	CreateGroupRole(role string) error
	RenameGroupRole(currentRole, newRole string) error
	CreateUserRole(role, password string) (string, error)
	UpdatePassword(role, password string) error
	GrantRole(role, grantee string) error
	AlterDatabaseOwner(dbName, owner string) error
	ReassignDatabaseOwner(dbName, currentOwner, newOwner string) error
	SetSchemaPrivileges(schemaPrivileges PostgresSchemaPrivileges) error
	RevokeRole(role, revoked string) error
	AlterDefaultLoginRole(role, setRole string) error
	DropDatabase(db string) error
	DropRole(role, newOwner, database string) error
	GetUser() string
	GetDefaultDatabase() string
}

type pg struct {
	db              *sql.DB
	log             logr.Logger
	host            string
	user            string
	pass            string
	args            string
	defaultDatabase string
	useIAMAuth      bool
	awsRegion       string
}

// PostgresSchemaPrivileges holds the parameters for setting schema-level privileges.
type PostgresSchemaPrivileges struct {
	DB            string
	Role          string
	CreatorRole   string
	Schema        string
	Privs         string
	SequencePrivs string
	FunctionPrivs string
	CreateSchema  bool
}

// generateIAMAuthToken is a package-level variable so tests can replace it.
var generateIAMAuthToken = defaultGenerateIAMAuthToken

func defaultGenerateIAMAuthToken(ctx context.Context, user, host, region string) (string, error) {
	// Parse host:port
	hostname, portStr, err := net.SplitHostPort(host)
	if err != nil {
		// No port specified, assume default PostgreSQL port
		hostname = host
		portStr = "5432"
	}

	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return "", fmt.Errorf("invalid port in host %q: %w", host, err)
	}

	// Build endpoint in hostname:port format
	endpoint := fmt.Sprintf("%s:%d", hostname, port)

	// Load AWS config (picks up IRSA credentials automatically)
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return "", fmt.Errorf("failed to load AWS config: %w", err)
	}

	token, err := rdsauth.BuildAuthToken(ctx, endpoint, region, user, awsCfg.Credentials)
	if err != nil {
		return "", fmt.Errorf("failed to generate RDS IAM auth token: %w", err)
	}

	return token, nil
}

// NewPG creates a new PG instance connected to the PostgreSQL server.
func NewPG(cfg *config.Cfg, logger logr.Logger) (PG, error) {
	password := cfg.PostgresPass

	// When IAM auth is enabled, generate a token to use as the password
	if cfg.PostgresUseIAMAuth && cfg.CloudProvider == config.CloudProviderAWS {
		logger.Info("Using IAM database authentication for RDS")
		token, err := generateIAMAuthToken(context.Background(), cfg.PostgresUser, cfg.PostgresHost, cfg.AwsRegion)
		if err != nil {
			return nil, fmt.Errorf("IAM auth token generation failed: %w", err)
		}
		password = token
	}

	db, err := GetConnection(
		cfg.PostgresUser,
		password,
		cfg.PostgresHost,
		cfg.PostgresDefaultDb,
		cfg.PostgresUriArgs)
	if err != nil {
		return nil, err
	}
	logger.V(1).Info("connected to postgres server")
	postgres := &pg{
		db:              db,
		log:             logger,
		host:            cfg.PostgresHost,
		user:            cfg.PostgresUser,
		pass:            password,
		args:            cfg.PostgresUriArgs,
		defaultDatabase: cfg.PostgresDefaultDb,
		useIAMAuth:      cfg.PostgresUseIAMAuth && cfg.CloudProvider == config.CloudProviderAWS,
		awsRegion:       cfg.AwsRegion,
	}

	switch cfg.CloudProvider {
	case config.CloudProviderAWS:
		logger.Info("Using AWS wrapper")
		return newAWSPG(postgres), nil
	case config.CloudProviderAzure:
		logger.Info("Using Azure wrapper")
		return newAzurePG(postgres), nil
	case config.CloudProviderGCP:
		logger.Info("Using GCP wrapper")
		return newGCPPG(postgres), nil
	default:
		logger.Info("Using default postgres implementation")
		return postgres, nil
	}
}

func (c *pg) GetUser() string {
	return c.user
}

func (c *pg) GetDefaultDatabase() string {
	return c.defaultDatabase
}

// GetConnection opens a new connection to the specified PostgreSQL database.
// When useIAMAuth is true and the cloud provider is AWS, it generates an IAM
// auth token to use as the password instead of the static password.
func GetConnection(user, password, host, database, uriArgs string) (*sql.DB, error) {
	db, err := sql.Open("postgres", fmt.Sprintf("postgresql://%s:%s@%s/%s?%s", user, password, host, database, uriArgs))
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	return db, err
}

// getConnection opens a connection to the specified database, generating a fresh
// IAM auth token if IAM authentication is enabled.
func (c *pg) getConnection(database string) (*sql.DB, error) {
	if c.useIAMAuth {
		return c.getConnectionWithIAMAuth(database)
	}
	return GetConnection(c.user, c.pass, c.host, database, c.args)
}

// getConnectionWithIAMAuth opens a connection using IAM database authentication.
// It generates a fresh IAM auth token for each connection.
func (c *pg) getConnectionWithIAMAuth(database string) (*sql.DB, error) {
	token, err := generateIAMAuthToken(context.Background(), c.user, c.host, c.awsRegion)
	if err != nil {
		return nil, fmt.Errorf("IAM auth token generation failed: %w", err)
	}
	return GetConnection(c.user, token, c.host, database, c.args)
}

// parseHostPort extracts hostname and port from a host:port string.
// Returns the hostname, port, and any error.
func parseHostPort(host string) (string, int32, error) {
	hostname, portStr, err := net.SplitHostPort(host)
	if err != nil {
		// No port specified, assume default PostgreSQL port
		hostname = host
		portStr = "5432"
	}

	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in host %q: %w", host, err)
	}

	return hostname, int32(port), nil
}

// isAWSRDS checks if the host looks like an RDS endpoint.
func isAWSRDS(host string) bool {
	return strings.Contains(host, ".rds.amazonaws.com")
}
