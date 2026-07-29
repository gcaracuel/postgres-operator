package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/movetokube/postgres-operator/api/v1alpha1"
	"github.com/movetokube/postgres-operator/pkg/config"
	mockpg "github.com/movetokube/postgres-operator/pkg/postgres/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("PostgresExternalUserReconciler", func() {
	const (
		name      = "test-external-role"
		namespace = "operator"
		dbName    = "test-db"
		roleName  = "my-external-iam-role"
	)
	var (
		sc       *runtime.Scheme
		req      reconcile.Request
		mockCtrl *gomock.Controller
		pg       *mockpg.MockPG
		rp       *PostgresExternalUserReconciler
		cl       client.Client
	)

	// Helper: create a Postgres CR that the external role references
	createPostgresCR := func(succeeded bool) *v1alpha1.Postgres {
		pgCR := &v1alpha1.Postgres{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dbName,
				Namespace: namespace,
			},
			Spec: v1alpha1.PostgresSpec{
				Database: dbName,
			},
			Status: v1alpha1.PostgresStatus{
				Succeeded: succeeded,
				Roles: v1alpha1.PostgresRoles{
					Owner:  dbName + "-owner",
					Reader: dbName + "-reader",
					Writer: dbName + "-writer",
				},
			},
		}
		Expect(cl.Create(ctx, pgCR)).To(BeNil())
		statusCopy := pgCR.Status.DeepCopy()
		Expect(cl.Status().Update(ctx, pgCR)).To(BeNil())
		statusCopy.DeepCopyInto(&pgCR.Status)
		return pgCR
	}

	// Helper: create a PostgresExternalUser CR
	createExternalRoleCR := func(privileges string, extraRoles []string, createIfNotExists bool, markAsDeleted bool) *v1alpha1.PostgresExternalUser {
		cr := &v1alpha1.PostgresExternalUser{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: v1alpha1.PostgresExternalUserSpec{
				RoleName:          roleName,
				Database:          dbName,
				Privileges:        privileges,
				ExtraRoles:        extraRoles,
				CreateIfNotExists: createIfNotExists,
			},
			Status: v1alpha1.PostgresExternalUserStatus{},
		}
		if markAsDeleted {
			cr.SetFinalizers([]string{"finalizer.db.movetokube.com"})
		}
		Expect(cl.Create(ctx, cr)).To(BeNil())
		if markAsDeleted {
			Expect(cl.Delete(ctx, cr, &client.DeleteOptions{GracePeriodSeconds: new(int64)})).To(BeNil())
		}
		return cr
	}

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		pg = mockpg.NewMockPG(mockCtrl)
		cl = k8sClient

		sc = scheme.Scheme
		sc.AddKnownTypes(v1alpha1.GroupVersion, &v1alpha1.PostgresExternalUser{})
		sc.AddKnownTypes(v1alpha1.GroupVersion, &v1alpha1.PostgresExternalUserList{})
		sc.AddKnownTypes(v1alpha1.GroupVersion, &v1alpha1.Postgres{})
		sc.AddKnownTypes(v1alpha1.GroupVersion, &v1alpha1.PostgresList{})

		rp = &PostgresExternalUserReconciler{
			Client: managerClient,
			Scheme: sc,
			pg:     pg,
		}

		req = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			},
		}
	})

	AfterEach(func() {
		// Clean up all PostgresExternalUser CRs
		l := v1alpha1.PostgresExternalUserList{}
		Expect(cl.List(ctx, &l)).NotTo(HaveOccurred())
		for _, el := range l.Items {
			org := el.DeepCopy()
			el.SetFinalizers(nil)
			Expect(cl.Patch(ctx, &el, client.MergeFrom(org))).To(BeNil())
		}
		Expect(cl.DeleteAllOf(ctx, &v1alpha1.PostgresExternalUser{}, client.InNamespace(namespace))).To(BeNil())

		// Clean up Postgres CRs
		pl := v1alpha1.PostgresList{}
		Expect(cl.List(ctx, &pl)).NotTo(HaveOccurred())
		for _, el := range pl.Items {
			org := el.DeepCopy()
			el.SetFinalizers(nil)
			Expect(cl.Patch(ctx, &el, client.MergeFrom(org))).To(BeNil())
		}
		Expect(cl.DeleteAllOf(ctx, &v1alpha1.Postgres{}, client.InNamespace(namespace))).To(BeNil())

		mockCtrl.Finish()
	})

	It("should not requeue if CR does not exist", func() {
		res, err := rp.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Requeue).To(BeFalse())
	})

	It("should skip if annotation filter does not match", func() {
		rp.instanceFilter = "other-instance"
		createExternalRoleCR("READ", nil, true, false)
		res, err := rp.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Requeue).To(BeFalse())
	})

	Context("Referenced Postgres CR does not exist", func() {
		BeforeEach(func() {
			createExternalRoleCR("READ", nil, true, false)
		})

		It("should skip reconciliation without error", func() {
			res, err := rp.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Requeue).To(BeFalse())
		})
	})

	Context("Referenced Postgres CR is not ready", func() {
		BeforeEach(func() {
			createPostgresCR(false) // not succeeded
			createExternalRoleCR("READ", nil, true, false)
		})

		It("should requeue with error", func() {
			_, err := rp.Reconcile(ctx, req)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not ready"))
		})
	})

	Describe("Creation logic", func() {
		BeforeEach(func() {
			createPostgresCR(true)
		})

		Context("Privileges = READ", func() {
			BeforeEach(func() {
				createExternalRoleCR("READ", nil, true, false)
			})

			It("should grant reader group role", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.Status.Succeeded).To(BeTrue())
				Expect(found.Status.RoleName).To(Equal(roleName))
				Expect(found.Status.GroupRole).To(Equal(dbName + "-reader"))
				Expect(found.Status.DatabaseName).To(Equal(dbName))
			})
		})

		Context("Privileges = WRITE", func() {
			BeforeEach(func() {
				createExternalRoleCR("WRITE", nil, true, false)
			})

			It("should grant writer group role", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-writer", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.Status.GroupRole).To(Equal(dbName + "-writer"))
			})
		})

		Context("Privileges = OWNER", func() {
			BeforeEach(func() {
				createExternalRoleCR("OWNER", nil, true, false)
			})

			It("should grant owner group role", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-owner", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.Status.GroupRole).To(Equal(dbName + "-owner"))
			})
		})

		Context("createIfNotExists = false", func() {
			BeforeEach(func() {
				createExternalRoleCR("READ", nil, false, false)
			})

			It("should not call CreateGroupRole", func() {
				pg.EXPECT().CreateGroupRole(roleName).Times(0)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())
			})
		})

		Context("with extra roles", func() {
			BeforeEach(func() {
				createExternalRoleCR("READ", []string{"rds_iam"}, true, false)
			})

			It("should grant extra roles", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().GrantRole("rds_iam", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.Status.GrantedExtraRoles).To(ConsistOf("rds_iam"))
			})
		})

		Context("CreateGroupRole fails", func() {
			BeforeEach(func() {
				createExternalRoleCR("READ", nil, true, false)
			})

			It("should requeue with error", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(fmt.Errorf("create role failed"))

				_, err := rp.Reconcile(ctx, req)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("create role"))
			})
		})

		Context("GrantRole fails", func() {
			BeforeEach(func() {
				createExternalRoleCR("READ", nil, true, false)
			})

			It("should requeue with error", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(fmt.Errorf("grant failed"))

				_, err := rp.Reconcile(ctx, req)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("grant role"))
			})
		})

		Context("IAM auth enable", func() {
			BeforeEach(func() {
				cr := createExternalRoleCR("READ", nil, true, false)
				cr.Spec.AWS = &v1alpha1.PostgresExternalUserAWSSpec{EnableIamAuth: true}
				Expect(cl.Update(ctx, cr)).To(BeNil())
				rp.cloudProvider = config.CloudProviderAWS
			})

			It("should grant rds_iam role", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().GrantRole("rds_iam", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.Status.EnableIamAuth).To(BeTrue())
				Expect(found.Status.Message).To(ContainSubstring("IAM auth enabled"))
			})
		})

		Context("IAM auth disable", func() {
			BeforeEach(func() {
				cr := createExternalRoleCR("READ", nil, true, false)
				cr.Status = v1alpha1.PostgresExternalUserStatus{
					Succeeded:     true,
					RoleName:      roleName,
					GroupRole:     dbName + "-reader",
					DatabaseName:  dbName,
					EnableIamAuth: true,
				}
				Expect(cl.Status().Update(ctx, cr)).To(BeNil())
				rp.cloudProvider = config.CloudProviderAWS
			})

			It("should revoke rds_iam role", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().RevokeRole("rds_iam", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.Status.EnableIamAuth).To(BeFalse())
			})
		})

		Context("IAM auth requested but not AWS provider", func() {
			BeforeEach(func() {
				cr := createExternalRoleCR("READ", nil, true, false)
				cr.Spec.AWS = &v1alpha1.PostgresExternalUserAWSSpec{EnableIamAuth: true}
				Expect(cl.Update(ctx, cr)).To(BeNil())
				rp.cloudProvider = config.CloudProviderNone
			})

			It("should not grant rds_iam and log a warning", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.Status.EnableIamAuth).To(BeFalse())
			})
		})

		Context("extra role grant fails", func() {
			BeforeEach(func() {
				createExternalRoleCR("READ", []string{"rds_iam", "another_role"}, true, false)
			})

			It("should continue and not fail the reconcile", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				// rds_iam fails, but another_role succeeds
				pg.EXPECT().GrantRole("rds_iam", roleName).Return(fmt.Errorf("rds_iam grant failed"))
				pg.EXPECT().GrantRole("another_role", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				// Only the successful extra role should be in status
				Expect(found.Status.GrantedExtraRoles).To(ConsistOf("another_role"))
			})
		})

		Context("should add finalizer on success", func() {
			BeforeEach(func() {
				createExternalRoleCR("READ", nil, true, false)
			})

			It("should set finalizer", func() {
				pg.EXPECT().CreateGroupRole(roleName).Return(nil)
				pg.EXPECT().GrantRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().GetDefaultDatabase().Return("postgres").AnyTimes()

				_, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				found := &v1alpha1.PostgresExternalUser{}
				Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)).To(BeNil())
				Expect(found.GetFinalizers()).To(ContainElement("finalizer.db.movetokube.com"))
			})
		})
	})

	Describe("Deletion logic", func() {
		BeforeEach(func() {
			createPostgresCR(true)
		})

		Context("role was previously created successfully", func() {
			BeforeEach(func() {
				cr := createExternalRoleCR("READ", []string{"rds_iam"}, true, false)
				cr.Status = v1alpha1.PostgresExternalUserStatus{
					Succeeded:     true,
					RoleName:      roleName,
					GroupRole:     dbName + "-reader",
					DatabaseName:  dbName,
					GrantedExtraRoles: []string{"rds_iam"},
				}
				Expect(cl.Status().Update(ctx, cr)).To(BeNil())
				// Now mark for deletion
				cr.SetFinalizers([]string{"finalizer.db.movetokube.com"})
				Expect(cl.Update(ctx, cr)).To(BeNil())
				Expect(cl.Delete(ctx, cr, &client.DeleteOptions{GracePeriodSeconds: new(int64)})).To(BeNil())
			})

			It("should revoke group role, extra roles, and drop role", func() {
				pg.EXPECT().GetDefaultDatabase().Return("postgres")
				pg.EXPECT().RevokeRole(dbName+"-reader", roleName).Return(nil)
				pg.EXPECT().RevokeRole("rds_iam", roleName).Return(nil)
				pg.EXPECT().DropRole(roleName, "pguser", dbName).Return(nil)
				pg.EXPECT().GetUser().Return("pguser")

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())

				// CR should be deleted (no finalizer = garbage collected)
				found := &v1alpha1.PostgresExternalUser{}
				err = cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, found)
				Expect(errors.IsNotFound(err)).To(BeTrue())
			})
		})

		Context("role was not yet created (no status)", func() {
			BeforeEach(func() {
				cr := &v1alpha1.PostgresExternalUser{
					ObjectMeta: metav1.ObjectMeta{
						Name:       name,
						Namespace:  namespace,
						Finalizers: []string{"finalizer.db.movetokube.com"},
					},
					Spec: v1alpha1.PostgresExternalUserSpec{
						RoleName:          roleName,
						Database:          dbName,
						Privileges:        "READ",
						CreateIfNotExists: true,
					},
					Status: v1alpha1.PostgresExternalUserStatus{},
				}
				Expect(cl.Create(ctx, cr)).To(BeNil())
				Expect(cl.Delete(ctx, cr, &client.DeleteOptions{GracePeriodSeconds: new(int64)})).To(BeNil())
			})

			It("should just remove finalizer without calling PG", func() {
				pg.EXPECT().GetDefaultDatabase().Times(0)

				res, err := rp.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())
				Expect(res.Requeue).To(BeFalse())
			})
		})
	})
})
