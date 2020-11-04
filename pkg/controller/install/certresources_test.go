package install

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/wrappers"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	listerfakes "github.com/operator-framework/operator-lifecycle-manager/pkg/fakes/client-go/listers"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/operatorclientmocks"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func keyPair(t *testing.T, expiration time.Time) *certs.KeyPair {
	p, err := certs.GenerateCA(expiration, Organization)
	assert.NoError(t, err)
	return p
}

func selector(t *testing.T, selector string) *metav1.LabelSelector {
	s, err := metav1.ParseToLabelSelector(selector)
	assert.NoError(t, err)
	return s
}

var staticCerts *certs.KeyPair = nil

// staticCertGenerator replaces the CertGenerator to get consistent keys for testing
func staticCertGenerator(notAfter time.Time, organization string, ca *certs.KeyPair, hosts []string) (*certs.KeyPair, error) {
	if staticCerts != nil {
		return staticCerts, nil
	}
	c, err := certs.CreateSignedServingPair(notAfter, organization, ca, hosts)
	if err != nil {
		return nil, err
	}
	staticCerts = c
	return staticCerts, nil
}

type fakeState struct {
	existingService *corev1.Service
	getServiceError error

	existingSecret *corev1.Secret
	getSecretError error

	existingRole *rbacv1.Role
	getRoleError error

	existingRoleBinding *rbacv1.RoleBinding
	getRoleBindingError error

	existingClusterRoleBinding *rbacv1.ClusterRoleBinding
	getClusterRoleBindingError error
}

func newFakeLister(state fakeState) *operatorlisterfakes.FakeOperatorLister {
	fakeLister := &operatorlisterfakes.FakeOperatorLister{}
	fakeCoreV1Lister := &operatorlisterfakes.FakeCoreV1Lister{}
	fakeRbacV1Lister := &operatorlisterfakes.FakeRbacV1Lister{}
	fakeLister.CoreV1Returns(fakeCoreV1Lister)
	fakeLister.RbacV1Returns(fakeRbacV1Lister)

	fakeServiceLister := &listerfakes.FakeServiceLister{}
	fakeCoreV1Lister.ServiceListerReturns(fakeServiceLister)
	fakeServiceNamespacedLister := &listerfakes.FakeServiceNamespaceLister{}
	fakeServiceLister.ServicesReturns(fakeServiceNamespacedLister)
	fakeServiceNamespacedLister.GetReturns(state.existingService, state.getServiceError)

	fakeSecretLister := &listerfakes.FakeSecretLister{}
	fakeCoreV1Lister.SecretListerReturns(fakeSecretLister)
	fakeSecretNamespacedLister := &listerfakes.FakeSecretNamespaceLister{}
	fakeSecretLister.SecretsReturns(fakeSecretNamespacedLister)
	fakeSecretNamespacedLister.GetReturns(state.existingSecret, state.getSecretError)

	fakeRoleLister := &listerfakes.FakeRoleLister{}
	fakeRbacV1Lister.RoleListerReturns(fakeRoleLister)
	fakeRoleNamespacedLister := &listerfakes.FakeRoleNamespaceLister{}
	fakeRoleLister.RolesReturns(fakeRoleNamespacedLister)
	fakeRoleNamespacedLister.GetReturns(state.existingRole, state.getRoleError)

	fakeRoleBindingLister := &listerfakes.FakeRoleBindingLister{}
	fakeRbacV1Lister.RoleBindingListerReturns(fakeRoleBindingLister)
	fakeRoleBindingNamespacedLister := &listerfakes.FakeRoleBindingNamespaceLister{}
	fakeRoleBindingLister.RoleBindingsReturns(fakeRoleBindingNamespacedLister)
	fakeRoleBindingNamespacedLister.GetReturns(state.existingRoleBinding, state.getRoleBindingError)

	fakeClusterRoleBindingLister := &listerfakes.FakeClusterRoleBindingLister{}
	fakeRbacV1Lister.ClusterRoleBindingListerReturns(fakeClusterRoleBindingLister)
	fakeClusterRoleBindingLister.GetReturns(state.existingClusterRoleBinding, state.getClusterRoleBindingError)

	return fakeLister
}

func TestInstallCertRequirementsForDeployment(t *testing.T) {
	ca := keyPair(t, time.Now().Add(time.Hour))
	caPEM, _, err := ca.ToPEM()
	assert.NoError(t, err)
	caHash := certs.PEMSHA256(caPEM)
	type fields struct {
		strategyClient         wrappers.InstallStrategyDeploymentInterface
		owner                  ownerutil.Owner
		previousStrategy       Strategy
		templateAnnotations    map[string]string
		initializers           DeploymentInitializerFuncChain
		apiServiceDescriptions []certResource
		webhookDescriptions    []certResource
	}
	type args struct {
		deploymentName string
		ca             *certs.KeyPair
		rotateAt       time.Time
		depSpec        appsv1.DeploymentSpec
		ports          []corev1.ServicePort
	}

	type expectedExternalFunc func(clientInterface *operatorclientmocks.MockClientInterface, fakeLister *operatorlisterfakes.FakeOperatorLister, namespace string, args args)
	tests := []struct {
		name         string
		mockExternal expectedExternalFunc
		state        fakeState
		fields       fields
		args         args
		want         *appsv1.DeploymentSpec
		wantErr      bool
	}{
		{
			name: "adds certs to deployment spec",
			mockExternal: func(mockOpClient *operatorclientmocks.MockClientInterface, fakeLister *operatorlisterfakes.FakeOperatorLister, namespace string, args args) {
				mockOpClient.EXPECT().DeleteService(namespace, "test-service", &metav1.DeleteOptions{}).Return(nil)
				service := corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "test-service",
						OwnerReferences: []metav1.OwnerReference{
							ownerutil.NonBlockingOwner(&v1alpha1.ClusterServiceVersion{}),
						},
					},
					Spec: corev1.ServiceSpec{
						Ports:    args.ports,
						Selector: selector(t, "test=label").MatchLabels,
					},
				}
				mockOpClient.EXPECT().CreateService(&service).Return(&service, nil)

				hosts := []string{
					fmt.Sprintf("%s.%s", service.GetName(), namespace),
					fmt.Sprintf("%s.%s.svc", service.GetName(), namespace),
				}
				servingPair, err := certGenerator.Generate(args.rotateAt, Organization, args.ca, hosts)
				require.NoError(t, err)

				// Create Secret for serving cert
				certPEM, privPEM, err := servingPair.ToPEM()
				require.NoError(t, err)

				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-service-cert",
						Namespace: namespace,
						Annotations: map[string]string{OLMCAHashAnnotationKey: caHash},
					},
					Data: map[string][]byte{
						"tls.crt": certPEM,
						"tls.key": privPEM,
					},
					Type: corev1.SecretTypeTLS,
				}
				mockOpClient.EXPECT().UpdateSecret(secret).Return(secret, nil)

				secretRole := &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.GetName(),
						Namespace: namespace,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{secret.GetName()},
						},
					},
				}
				mockOpClient.EXPECT().UpdateRole(secretRole).Return(secretRole, nil)

				roleBinding := &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.GetName(),
						Namespace: namespace,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      "test-sa",
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     secretRole.GetName(),
					},
				}
				mockOpClient.EXPECT().UpdateRoleBinding(roleBinding).Return(roleBinding, nil)

				authDelegatorClusterRoleBinding := &rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name: service.GetName() + "-system:auth-delegator",
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      "test-sa",
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "ClusterRole",
						Name:     "system:auth-delegator",
					},
				}

				mockOpClient.EXPECT().UpdateClusterRoleBinding(authDelegatorClusterRoleBinding).Return(authDelegatorClusterRoleBinding, nil)

				authReaderRoleBinding := &rbacv1.RoleBinding{
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							APIGroup:  "",
							Name:      args.depSpec.Template.Spec.ServiceAccountName,
							Namespace: namespace,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     "extension-apiserver-authentication-reader",
					},
				}
				authReaderRoleBinding.SetName(service.GetName() + "-auth-reader")
				authReaderRoleBinding.SetNamespace(KubeSystem)

				mockOpClient.EXPECT().UpdateRoleBinding(authReaderRoleBinding).Return(authReaderRoleBinding, nil)
			},
			state: fakeState{
				existingService: &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						OwnerReferences: []metav1.OwnerReference{
							ownerutil.NonBlockingOwner(&v1alpha1.ClusterServiceVersion{}),
						},
					},
				},
				existingSecret: &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{},
				},
				existingRole: &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{},
				},
				existingRoleBinding: &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{},
				},
				existingClusterRoleBinding: &rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{},
				},
			},
			fields: fields{
				owner:                  &v1alpha1.ClusterServiceVersion{},
				previousStrategy:       nil,
				templateAnnotations:    nil,
				initializers:           nil,
				apiServiceDescriptions: []certResource{},
				webhookDescriptions:    []certResource{},
			},
			args: args{
				deploymentName: "test",
				ca:             ca,
				rotateAt:       time.Now().Add(time.Hour),
				ports:          []corev1.ServicePort{},
				depSpec: appsv1.DeploymentSpec{
					Selector: selector(t, "test=label"),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							ServiceAccountName: "test-sa",
						},
					},
				},
			},
			want: &appsv1.DeploymentSpec{
				Selector: selector(t, "test=label"),
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{OLMCAHashAnnotationKey: caHash},
					},
					Spec: corev1.PodSpec{
						ServiceAccountName: "test-sa",
						Volumes: []corev1.Volume{
							{
								Name: "apiservice-cert",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "test-service-cert",
										Items: []corev1.KeyToPath{
											{
												Key:  "tls.crt",
												Path: "apiserver.crt",
											},
											{
												Key:  "tls.key",
												Path: "apiserver.key",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			certGenerator = certs.CertGeneratorFunc(staticCertGenerator)

			mockOpClient := operatorclientmocks.NewMockClientInterface(ctrl)
			fakeLister := newFakeLister(tt.state)
			tt.mockExternal(mockOpClient, fakeLister, tt.fields.owner.GetNamespace(), tt.args)

			client := wrappers.NewInstallStrategyDeploymentClient(mockOpClient, fakeLister, tt.fields.owner.GetNamespace())
			i := &StrategyDeploymentInstaller{
				strategyClient:         client,
				owner:                  tt.fields.owner,
				previousStrategy:       tt.fields.previousStrategy,
				templateAnnotations:    tt.fields.templateAnnotations,
				initializers:           tt.fields.initializers,
				apiServiceDescriptions: tt.fields.apiServiceDescriptions,
				webhookDescriptions:    tt.fields.webhookDescriptions,
			}
			got, err := i.installCertRequirementsForDeployment(tt.args.deploymentName, tt.args.ca, tt.args.rotateAt, tt.args.depSpec, tt.args.ports)
			if (err != nil) != tt.wantErr {
				t.Errorf("installCertRequirementsForDeployment() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("installCertRequirementsForDeployment() got = %v, want %v", got, tt.want)
			}
		})
	}
}
