package postgres

import (
	"fmt"
	"strings"
	"testing"
)

func TestDefaultPrivilegesSQL(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		args     []interface{}
		expected string
	}{
		{
			name:   "DEFAULT_PRIVS_SCHEMA includes FOR ROLE",
			format: DEFAULT_PRIVS_SCHEMA,
			args:   []interface{}{"owner-role", "public", "SELECT", "reader-role"},
			expected: `ALTER DEFAULT PRIVILEGES FOR ROLE "owner-role" IN SCHEMA "public" GRANT SELECT ON TABLES TO "reader-role"`,
		},
		{
			name:   "DEFAULT_PRIVS_SCHEMA with writer privs",
			format: DEFAULT_PRIVS_SCHEMA,
			args:   []interface{}{"owner-role", "public", "SELECT,INSERT,DELETE,UPDATE", "writer-role"},
			expected: `ALTER DEFAULT PRIVILEGES FOR ROLE "owner-role" IN SCHEMA "public" GRANT SELECT,INSERT,DELETE,UPDATE ON TABLES TO "writer-role"`,
		},
		{
			name:   "DEFAULT_PRIVS_SCHEMA with writer as creator",
			format: DEFAULT_PRIVS_SCHEMA,
			args:   []interface{}{"writer-role", "public", "SELECT", "reader-role"},
			expected: `ALTER DEFAULT PRIVILEGES FOR ROLE "writer-role" IN SCHEMA "public" GRANT SELECT ON TABLES TO "reader-role"`,
		},
		{
			name:   "DEFAULT_PRIVS_FUNCTIONS includes FOR ROLE",
			format: DEFAULT_PRIVS_FUNCTIONS,
			args:   []interface{}{"owner-role", "public", "EXECUTE", "writer-role"},
			expected: `ALTER DEFAULT PRIVILEGES FOR ROLE "owner-role" IN SCHEMA "public" GRANT EXECUTE ON FUNCTIONS TO "writer-role"`,
		},
		{
			name:   "DEFAULT_PRIVS_SEQUENCES includes FOR ROLE",
			format: DEFAULT_PRIVS_SEQUENCES,
			args:   []interface{}{"owner-role", "public", "USAGE,SELECT", "writer-role"},
			expected: `ALTER DEFAULT PRIVILEGES FOR ROLE "owner-role" IN SCHEMA "public" GRANT USAGE,SELECT ON SEQUENCES TO "writer-role"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmt.Sprintf(tt.format, tt.args...)
			if got != tt.expected {
				t.Errorf("got:  %s\nwant: %s", got, tt.expected)
			}
		})
	}
}

func TestDefaultPrivilegesConstantsContainForRole(t *testing.T) {
	constants := map[string]string{
		"DEFAULT_PRIVS_SCHEMA":    DEFAULT_PRIVS_SCHEMA,
		"DEFAULT_PRIVS_FUNCTIONS": DEFAULT_PRIVS_FUNCTIONS,
		"DEFAULT_PRIVS_SEQUENCES": DEFAULT_PRIVS_SEQUENCES,
	}

	for name, format := range constants {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(format, "FOR ROLE") {
				t.Errorf("%s is missing FOR ROLE clause: %s", name, format)
			}
		})
	}
}
