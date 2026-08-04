package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresExternalUserSpec defines the desired state of PostgresExternalUser
type PostgresExternalUserSpec struct {
	// Fixed name of the role to create or assign permissions to.
	// If the role already exists in PostgreSQL, it will be used as-is.
	// If it does not exist, it will be created without a password (suitable for IAM-managed roles).
	RoleName string `json:"roleName"`
	// Name of the Postgres CR (database) this role will be associated with.
	Database string `json:"database"`
	// Privileges to grant: OWNER, READ, or WRITE.
	// OWNER grants all privileges on the database schemas.
	// READ grants SELECT on all tables in the schemas.
	// WRITE grants SELECT, INSERT, DELETE, UPDATE on all tables in the schemas.
	// +kubebuilder:validation:Enum=OWNER;READ;WRITE
	Privileges string `json:"privileges"`
	// Extra PostgreSQL roles to grant to this role (e.g., "rds_iam" for AWS IAM authentication).
	// +optional
	ExtraRoles []string `json:"extraRoles,omitempty"`
	// Whether to create the role if it does not exist in PostgreSQL.
	// If false and the role does not exist, the controller will not create it.
	// +optional
	// +kubebuilder:default=true
	CreateIfNotExists bool `json:"createIfNotExists,omitempty"`
	// AWS specific configuration.
	// +optional
	AWS *PostgresExternalUserAWSSpec `json:"aws,omitempty"`
}

// PostgresExternalUserAWSSpec encapsulates AWS specific configuration toggles for PostgresExternalUser.
type PostgresExternalUserAWSSpec struct {
	// +optional
	// +kubebuilder:default=false
	// Enable IAM authentication for this role (PostgreSQL on AWS RDS only).
	// Grants the rds_iam role to this role, allowing IAM database authentication.
	EnableIamAuth bool `json:"enableIamAuth,omitempty"`
}

// PostgresExternalUserStatus defines the observed state of PostgresExternalUser
type PostgresExternalUserStatus struct {
	Succeeded bool `json:"succeeded"`
	// The role name as it exists in PostgreSQL.
	RoleName string `json:"roleName"`
	// The group role (OWNER/READ/WRITE) this role was granted.
	GroupRole string `json:"groupRole"`
	// The database name this role is associated with.
	DatabaseName string `json:"databaseName"`
	// Extra roles that have been granted to this role.
	// +optional
	GrantedExtraRoles []string `json:"grantedExtraRoles,omitempty"`
	// Reflects whether IAM authentication is enabled for this role.
	// +optional
	EnableIamAuth bool `json:"enableIamAuth"`
	// Human-readable message describing the last action taken on the database.
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// PostgresExternalUser is the Schema for the postgresexternalusers API.
// It manages fixed-name PostgreSQL roles without passwords, suitable for
// externally-managed users (e.g., IAM-authenticated roles on AWS RDS).
type PostgresExternalUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresExternalUserSpec   `json:"spec,omitempty"`
	Status PostgresExternalUserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresExternalUserList contains a list of PostgresExternalUser
type PostgresExternalUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresExternalUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresExternalUser{}, &PostgresExternalUserList{})
}
