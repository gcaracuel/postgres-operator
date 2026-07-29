package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dbv1alpha1 "github.com/movetokube/postgres-operator/api/v1alpha1"
	"github.com/movetokube/postgres-operator/pkg/config"
	"github.com/movetokube/postgres-operator/pkg/postgres"
	"github.com/movetokube/postgres-operator/pkg/utils"
)

// PostgresExternalUserReconciler reconciles a PostgresExternalUser object
type PostgresExternalUserReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	pg             postgres.PG
	instanceFilter string
	cloudProvider  config.CloudProvider
}

// NewPostgresExternalUserReconciler returns a new reconcile.Reconciler
func NewPostgresExternalUserReconciler(mgr manager.Manager, cfg *config.Cfg, pg postgres.PG) *PostgresExternalUserReconciler {
	return &PostgresExternalUserReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		pg:             pg,
		instanceFilter: cfg.AnnotationFilter,
		cloudProvider:  cfg.CloudProvider,
	}
}

// +kubebuilder:rbac:groups=db.movetokube.com,resources=postgresexternalusers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db.movetokube.com,resources=postgresexternalusers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=db.movetokube.com,resources=postgresexternalusers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop.
// It ensures the fixed-name role exists in PostgreSQL and is granted the correct permissions.
func (r *PostgresExternalUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	reqLogger := log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Reconciling PostgresExternalUser")

	instance := &dbv1alpha1.PostgresExternalUser{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !utils.MatchesInstanceAnnotation(instance.Annotations, r.instanceFilter) {
		return ctrl.Result{}, nil
	}

	// Deletion logic: revoke group role membership, drop the role if we created it
	if instance.GetDeletionTimestamp() != nil {
		return r.reconcileDelete(ctx, instance, reqLogger)
	}

	return r.reconcileCreate(ctx, instance, reqLogger)
}

func (r *PostgresExternalUserReconciler) reconcileDelete(ctx context.Context, instance *dbv1alpha1.PostgresExternalUser, reqLogger logr.Logger) (ctrl.Result, error) {
	if instance.Status.Succeeded && instance.Status.RoleName != "" {
		db := r.pg.GetDefaultDatabase()
		postgres, err := r.getPostgresCR(ctx, instance)
		if err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if postgres != nil && postgres.GetDeletionTimestamp().IsZero() {
			db = instance.Status.DatabaseName
		}

		// Revoke group role membership (always do this — it's what this CR added)
		if instance.Status.GroupRole != "" {
			if err := r.pg.RevokeRole(instance.Status.GroupRole, instance.Status.RoleName); err != nil {
				reqLogger.Error(err, "failed to revoke group role", "role", instance.Status.RoleName, "group", instance.Status.GroupRole)
			}
		}

		// Revoke extra roles, except rds_iam (managed by IAM flag, not by deletion)
		for _, extra := range instance.Status.GrantedExtraRoles {
			if extra == "rds_iam" {
				continue
			}
			if err := r.pg.RevokeRole(extra, instance.Status.RoleName); err != nil {
				reqLogger.Error(err, "failed to revoke extra role", "role", instance.Status.RoleName, "extra", extra)
			}
		}

		// Try to drop the role. If it has other role memberships or owns objects,
		// the DROP will fail — that's fine, we just leave the role in place.
		if err := r.pg.DropRole(instance.Status.RoleName, r.pg.GetUser(), db); err != nil {
			reqLogger.Info("role not dropped (may have other memberships or objects), group role revoked",
				"role", instance.Status.RoleName, "error", err)
		}
	}

	controllerutil.RemoveFinalizer(instance, "finalizer.db.movetokube.com")
	if err := r.Update(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *PostgresExternalUserReconciler) reconcileCreate(ctx context.Context, instance *dbv1alpha1.PostgresExternalUser, reqLogger logr.Logger) (ctrl.Result, error) {
	roleName := instance.Spec.RoleName
	if roleName == "" {
		return ctrl.Result{}, fmt.Errorf("roleName is required")
	}

	// Get the Postgres CR to find the group role name
	database, err := r.getPostgresCR(ctx, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("Referenced Postgres CR not found, skipping reconciliation",
				"database", instance.Spec.Database)
			return ctrl.Result{}, nil
		}
		return r.requeue(ctx, instance, err)
	}

	// Determine the target group role based on privileges
	var groupRole string
	switch instance.Spec.Privileges {
	case "READ":
		groupRole = database.Status.Roles.Reader
	case "WRITE":
		groupRole = database.Status.Roles.Writer
	default:
		groupRole = database.Status.Roles.Owner
	}

	if groupRole == "" {
		return r.requeue(ctx, instance, fmt.Errorf("target group role is empty for privileges=%s", instance.Spec.Privileges))
	}

	// Create the role if it doesn't exist (no password, no login)
	if instance.Spec.CreateIfNotExists {
		if err := r.pg.CreateGroupRole(roleName); err != nil {
			return r.requeue(ctx, instance, fmt.Errorf("create role %s: %w", roleName, err))
		}
	}

	// Grant the group role to the external role
	if err := r.pg.GrantRole(groupRole, roleName); err != nil {
		return r.requeue(ctx, instance, fmt.Errorf("grant role %s to %s: %w", groupRole, roleName, err))
	}

	// Grant extra roles
	var grantedExtras []string
	for _, extra := range instance.Spec.ExtraRoles {
		if err := r.pg.GrantRole(extra, roleName); err != nil {
			reqLogger.Error(err, "failed to grant extra role", "role", roleName, "extra", extra)
			continue
		}
		grantedExtras = append(grantedExtras, extra)
	}

	// Handle IAM auth
	awsConfig := instance.Spec.AWS
	awsIamRequested := awsConfig != nil && awsConfig.EnableIamAuth

	if r.cloudProvider == config.CloudProviderAWS {
		if awsIamRequested && !instance.Status.EnableIamAuth {
			if err := r.pg.GrantRole("rds_iam", roleName); err != nil {
				return r.requeue(ctx, instance, fmt.Errorf("grant rds_iam to %s: %w", roleName, err))
			}
			instance.Status.EnableIamAuth = true
			grantedExtras = append(grantedExtras, "rds_iam")
		}

		if !awsIamRequested && instance.Status.EnableIamAuth {
			if err := r.pg.RevokeRole("rds_iam", roleName); err != nil {
				return r.requeue(ctx, instance, fmt.Errorf("revoke rds_iam from %s: %w", roleName, err))
			}
			instance.Status.EnableIamAuth = false
		}
	} else if awsIamRequested {
		reqLogger.WithValues("role", roleName).Info("IAM Auth requested while not running with AWS cloud provider config")
	}

	// Build status message
	msg := fmt.Sprintf("Granted role '%s' to '%s'", groupRole, roleName)
	if len(grantedExtras) > 0 {
		msg += fmt.Sprintf("; granted extra roles: %v", grantedExtras)
	}
	if awsIamRequested {
		msg += "; IAM auth enabled"
	}

	// Update status
	instance.Status.RoleName = roleName
	instance.Status.GroupRole = groupRole
	instance.Status.DatabaseName = database.Spec.Database
	instance.Status.GrantedExtraRoles = grantedExtras
	instance.Status.Message = msg
	instance.Status.Succeeded = true
	if err := r.Status().Update(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	// Add finalizer
	if err := r.addFinalizer(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	reqLogger.Info("Reconciliation complete",
		"role", roleName,
		"groupRole", groupRole,
		"database", instance.Status.DatabaseName,
		"extraRoles", grantedExtras)
	return ctrl.Result{}, nil
}

func (r *PostgresExternalUserReconciler) getPostgresCR(ctx context.Context, instance *dbv1alpha1.PostgresExternalUser) (*dbv1alpha1.Postgres, error) {
	database := dbv1alpha1.Postgres{}
	err := r.Get(ctx,
		types.NamespacedName{Namespace: instance.Namespace, Name: instance.Spec.Database}, &database)
	if err != nil {
		return nil, err
	}
	if !utils.MatchesInstanceAnnotation(database.Annotations, r.instanceFilter) {
		return nil, fmt.Errorf("database %q is not managed by this operator", database.Name)
	}
	if !database.Status.Succeeded {
		return nil, fmt.Errorf("database %q is not ready", database.Name)
	}
	return &database, nil
}

func (r *PostgresExternalUserReconciler) addFinalizer(ctx context.Context, instance *dbv1alpha1.PostgresExternalUser) error {
	if len(instance.GetFinalizers()) < 1 && instance.GetDeletionTimestamp() == nil {
		instance.SetFinalizers([]string{"finalizer.db.movetokube.com"})
		return r.Update(ctx, instance)
	}
	return nil
}

func (r *PostgresExternalUserReconciler) requeue(ctx context.Context, cr *dbv1alpha1.PostgresExternalUser, reason error) (ctrl.Result, error) {
	cr.Status.Succeeded = false
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, reason
}

// findExternalUsersForPostgres returns reconcile requests for all PostgresExternalUsers
// that reference the given Postgres CR by name within the same namespace.
func (r *PostgresExternalUserReconciler) findExternalUsersForPostgres(ctx context.Context, obj client.Object) []reconcile.Request {
	postgres := obj.(*dbv1alpha1.Postgres)
	logger := log.FromContext(ctx)

	var list dbv1alpha1.PostgresExternalUserList
	if err := r.List(ctx, &list, client.InNamespace(postgres.Namespace)); err != nil {
		logger.Error(err, "Failed to list PostgresExternalUsers for Postgres CR", "postgres", postgres.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, item := range list.Items {
		if item.Spec.Database == postgres.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      item.Name,
					Namespace: item.Namespace,
				},
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *PostgresExternalUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1alpha1.PostgresExternalUser{}).
		Watches(
			&dbv1alpha1.Postgres{},
			handler.EnqueueRequestsFromMapFunc(r.findExternalUsersForPostgres),
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					pg := e.Object.(*dbv1alpha1.Postgres)
					return pg.Status.Succeeded
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldPg := e.ObjectOld.(*dbv1alpha1.Postgres)
					newPg := e.ObjectNew.(*dbv1alpha1.Postgres)
					return !oldPg.Status.Succeeded && newPg.Status.Succeeded
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return true
				},
				GenericFunc: func(e event.GenericEvent) bool {
					return false
				},
			}),
		).
		Complete(r)
}
