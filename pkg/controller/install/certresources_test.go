package install

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"

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

func certificatePEM(t *testing.T, template, parent *x509.Certificate, publicKey any, signer crypto.Signer) []byte {
	raw, err := x509.CreateCertificate(rand.Reader, template, parent, publicKey, signer)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
}

func publicKeyFromPEM(t *testing.T, keyPEM []byte) any {
	block, _ := pem.Decode(keyPEM)
	require.NotNil(t, block)
	key, err := x509.ParseECPrivateKey(block.Bytes)
	require.NoError(t, err)
	return &key.PublicKey
}

func validServingSecret(t *testing.T) (*corev1.Secret, []string, *certs.KeyPair) {
	hosts := HostnamesForService("test-service", "test-namespace")
	ca := keyPair(t, time.Now().Add(DefaultCertValidFor))
	serving, err := certs.CreateSignedServingPair(time.Now().Add(DefaultCertValidFor), Organization, ca, hosts)
	require.NoError(t, err)

	caPEM, _, err := ca.ToPEM()
	require.NoError(t, err)
	certPEM, keyPEM, err := serving.ToPEM()
	require.NoError(t, err)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			OLMCAHashAnnotationKey:   certs.PEMSHA256(caPEM),
			OLMCertHashAnnotationKey: certs.PEMSHA256(certPEM),
			OLMKeyHashAnnotationKey:  certs.PEMSHA256(keyPEM),
		}},
		Data: map[string][]byte{
			OLMCAPEMKey:             caPEM,
			corev1.TLSCertKey:       certPEM,
			corev1.TLSPrivateKeyKey: keyPEM,
		},
	}, hosts, ca
}

func setCertificateHash(secret *corev1.Secret, key string) {
	secret.Annotations[key] = certs.PEMSHA256(secret.Data[map[string]string{
		OLMCAHashAnnotationKey:   OLMCAPEMKey,
		OLMCertHashAnnotationKey: corev1.TLSCertKey,
		OLMKeyHashAnnotationKey:  corev1.TLSPrivateKeyKey,
	}[key]])
}

func TestShouldRotateCerts(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(secret *corev1.Secret, hosts []string, ca *certs.KeyPair)
		wantRotate bool
	}{
		{
			name: "valid material and fingerprints",
		},
		{
			name: "missing CA",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				delete(secret.Data, OLMCAPEMKey)
			},
			wantRotate: true,
		},
		{
			name: "missing certificate",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				delete(secret.Data, corev1.TLSCertKey)
			},
			wantRotate: true,
		},
		{
			name: "missing private key",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				delete(secret.Data, corev1.TLSPrivateKeyKey)
			},
			wantRotate: true,
		},
		{
			name: "malformed CA",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				secret.Data[OLMCAPEMKey] = []byte("malformed")
			},
			wantRotate: true,
		},
		{
			name: "malformed certificate",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				secret.Data[corev1.TLSCertKey] = []byte("malformed")
			},
			wantRotate: true,
		},
		{
			name: "malformed private key",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				secret.Data[corev1.TLSPrivateKeyKey] = []byte("malformed")
			},
			wantRotate: true,
		},
		{
			name: "expired certificate",
			mutate: func(secret *corev1.Secret, _ []string, ca *certs.KeyPair) {
				servingCert, err := certs.PEMToCert(secret.Data[corev1.TLSCertKey])
				require.NoError(t, err)
				expired := *servingCert
				expired.NotBefore = time.Now().Add(-2 * time.Hour)
				expired.NotAfter = time.Now().Add(-time.Hour)
				secret.Data[corev1.TLSCertKey] = certificatePEM(t, &expired, ca.Cert, publicKeyFromPEM(t, secret.Data[corev1.TLSPrivateKeyKey]), ca.Priv)
				setCertificateHash(secret, OLMCertHashAnnotationKey)
			},
			wantRotate: true,
		},
		{
			name: "not yet valid certificate",
			mutate: func(secret *corev1.Secret, _ []string, ca *certs.KeyPair) {
				servingCert, err := certs.PEMToCert(secret.Data[corev1.TLSCertKey])
				require.NoError(t, err)
				future := *servingCert
				future.NotBefore = time.Now().Add(time.Hour)
				future.NotAfter = time.Now().Add(2 * time.Hour)
				secret.Data[corev1.TLSCertKey] = certificatePEM(t, &future, ca.Cert, publicKeyFromPEM(t, secret.Data[corev1.TLSPrivateKeyKey]), ca.Priv)
				setCertificateHash(secret, OLMCertHashAnnotationKey)
			},
			wantRotate: true,
		},
		{
			name: "wrong hostname",
			mutate: func(_ *corev1.Secret, hosts []string, _ *certs.KeyPair) {
				hosts[0] = "unexpected.example"
			},
			wantRotate: true,
		},
		{
			name: "wrong CA",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				wrongCA := keyPair(t, time.Now().Add(DefaultCertValidFor))
				caPEM, _, err := wrongCA.ToPEM()
				require.NoError(t, err)
				secret.Data[OLMCAPEMKey] = caPEM
				setCertificateHash(secret, OLMCAHashAnnotationKey)
			},
			wantRotate: true,
		},
		{
			name: "mismatched private key",
			mutate: func(secret *corev1.Secret, hosts []string, ca *certs.KeyPair) {
				other, err := certs.CreateSignedServingPair(time.Now().Add(DefaultCertValidFor), Organization, ca, hosts)
				require.NoError(t, err)
				_, keyPEM, err := other.ToPEM()
				require.NoError(t, err)
				secret.Data[corev1.TLSPrivateKeyKey] = keyPEM
				setCertificateHash(secret, OLMKeyHashAnnotationKey)
			},
			wantRotate: true,
		},
		{
			name: "changed CA fingerprint",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				secret.Annotations[OLMCAHashAnnotationKey] = "changed"
			},
			wantRotate: true,
		},
		{
			name: "changed certificate fingerprint",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				secret.Annotations[OLMCertHashAnnotationKey] = "changed"
			},
			wantRotate: true,
		},
		{
			name: "changed private key fingerprint",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				secret.Annotations[OLMKeyHashAnnotationKey] = "changed"
			},
			wantRotate: true,
		},
		{
			name: "legacy secret without fingerprints",
			mutate: func(secret *corev1.Secret, _ []string, _ *certs.KeyPair) {
				secret.Annotations = nil
			},
			wantRotate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, hosts, ca := validServingSecret(t)
			if tt.mutate != nil {
				tt.mutate(secret, hosts, ca)
			}
			assert.Equal(t, tt.wantRotate, shouldRotateCerts(secret, hosts))
		})
	}
}

func selector(t *testing.T, selector string) *metav1.LabelSelector {
	s, err := metav1.ParseToLabelSelector(selector)
	assert.NoError(t, err)
	return s
}

var staticCerts *certs.KeyPair

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
	getSecret      func() (*corev1.Secret, error)

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
	if state.getSecret != nil {
		fakeSecretNamespacedLister.GetCalls(func(string) (*corev1.Secret, error) {
			return state.getSecret()
		})
	} else {
		fakeSecretNamespacedLister.GetReturns(state.existingSecret, state.getSecretError)
	}

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
	owner := ownerutil.Owner(&v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner",
			Namespace: "test-namespace",
			UID:       "123-uid",
		},
	})
	ca := keyPair(t, time.Now().Add(DefaultCertValidFor))
	caPEM, _, err := ca.ToPEM()
	assert.NoError(t, err)
	caHash := certs.PEMSHA256(caPEM)
	type fields struct {
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
		reuseSecret    bool
	}

	type expectedExternalFunc func(clientInterface *operatorclientmocks.MockClientInterface, fakeLister *operatorlisterfakes.FakeOperatorLister, namespace string, args args)
	var reusableSecret *corev1.Secret
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
			name: "reuses valid fingerprinted certs and adds certs to deployment spec",
			mockExternal: func(mockOpClient *operatorclientmocks.MockClientInterface, fakeLister *operatorlisterfakes.FakeOperatorLister, namespace string, args args) {
				service := corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-service",
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
						OwnerReferences: []metav1.OwnerReference{
							ownerutil.NonBlockingOwner(&v1alpha1.ClusterServiceVersion{}),
						},
					},
					Spec: corev1.ServiceSpec{
						Ports:    args.ports,
						Selector: selector(t, "test=label").MatchLabels,
					},
				}

				portsApplyConfig := []*corev1ac.ServicePortApplyConfiguration{}
				for _, p := range args.ports {
					ac := corev1ac.ServicePort().
						WithName(p.Name).
						WithPort(p.Port).
						WithTargetPort(p.TargetPort)
					portsApplyConfig = append(portsApplyConfig, ac)
				}

				svcApplyConfig := corev1ac.Service(service.Name, service.Namespace).
					WithSpec(corev1ac.ServiceSpec().
						WithPorts(portsApplyConfig...).
						WithSelector(selector(t, "test=label").MatchLabels)).
					WithOwnerReferences(ownerutil.NonBlockingOwnerApplyConfiguration(&v1alpha1.ClusterServiceVersion{})).
					WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

				mockOpClient.EXPECT().ApplyService(svcApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(&service, nil)

				hosts := []string{
					fmt.Sprintf("%s.%s", service.GetName(), namespace),
					fmt.Sprintf("%s.%s.svc", service.GetName(), namespace),
				}
				if args.reuseSecret {
					hosts = HostnamesForService(service.GetName(), namespace)
				}
				servingPair, err := certGenerator.Generate(args.rotateAt, Organization, args.ca, hosts)
				require.NoError(t, err)

				// Create Secret for serving cert
				certPEM, privPEM, err := servingPair.ToPEM()
				require.NoError(t, err)

				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service-cert",
						Namespace: namespace,
						Annotations: map[string]string{
							OLMCAHashAnnotationKey:   caHash,
							OLMCertHashAnnotationKey: certs.PEMSHA256(certPEM),
							OLMKeyHashAnnotationKey:  certs.PEMSHA256(privPEM),
						},
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
					},
					Data: map[string][]byte{
						corev1.TLSCertKey:       certPEM,
						corev1.TLSPrivateKeyKey: privPEM,
						OLMCAPEMKey:             caPEM,
					},
					Type: corev1.SecretTypeTLS,
				}
				if args.reuseSecret {
					reusableSecret = secret
				} else {
					mockOpClient.EXPECT().UpdateSecret(secret).Return(secret, nil)
				}

				secretRole := &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.GetName(),
						Namespace: namespace,
						Labels:    map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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
						Labels:    map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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
						Name:   service.GetName() + "-system:auth-delegator",
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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

				crbLabels := map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}
				for key, val := range ownerutil.OwnerLabel(ownerutil.Owner(&v1alpha1.ClusterServiceVersion{}), owner.GetObjectKind().GroupVersionKind().Kind) {
					crbLabels[key] = val
				}
				crbApplyConfig := rbacv1ac.ClusterRoleBinding(AuthDelegatorClusterRoleBindingName(service.GetName())).
					WithSubjects(rbacv1ac.Subject().
						WithKind("ServiceAccount").
						WithAPIGroup("").
						WithName(args.depSpec.Template.Spec.ServiceAccountName).
						WithNamespace("")). // Empty owner with no namespace
					WithRoleRef(rbacv1ac.RoleRef().
						WithAPIGroup("rbac.authorization.k8s.io").
						WithKind("ClusterRole").
						WithName("system:auth-delegator")).
					WithLabels(crbLabels)
				mockOpClient.EXPECT().ApplyClusterRoleBinding(crbApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(authDelegatorClusterRoleBinding, nil)

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
				authReaderRoleBinding.SetLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

				authReaderRoleBindingApplyConfig := rbacv1ac.RoleBinding(AuthReaderRoleBindingName(service.GetName()), KubeSystem).
					WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}).
					WithSubjects(rbacv1ac.Subject().
						WithKind("ServiceAccount").
						WithAPIGroup("").
						WithName(args.depSpec.Template.Spec.ServiceAccountName).
						WithNamespace(namespace)).
					WithRoleRef(rbacv1ac.RoleRef().
						WithAPIGroup("rbac.authorization.k8s.io").
						WithKind("Role").
						WithName("extension-apiserver-authentication-reader"))

				mockOpClient.EXPECT().ApplyRoleBinding(authReaderRoleBindingApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(authReaderRoleBinding, nil)
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
				getSecret: func() (*corev1.Secret, error) {
					return reusableSecret, nil
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
				rotateAt:       time.Now().Add(DefaultCertValidFor),
				reuseSecret:    true,
				ports:          []corev1.ServicePort{},
				depSpec: appsv1.DeploymentSpec{
					Selector: selector(t, "test=label"),
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							ServiceAccountName: "test-sa",
						},
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"foo": "bar",
							},
						},
					},
				},
			},
			want: &appsv1.DeploymentSpec{
				Selector: selector(t, "test=label"),
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"foo":                  "bar",
							OLMCAHashAnnotationKey: caHash},
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
												Key:  corev1.TLSCertKey,
												Path: "apiserver.crt",
											},
											{
												Key:  corev1.TLSPrivateKeyKey,
												Path: "apiserver.key",
											},
										},
									},
								},
							},
							{
								Name: "webhook-cert",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "test-service-cert",
										Items: []corev1.KeyToPath{
											{
												Key:  corev1.TLSCertKey,
												Path: "tls.crt",
											},
											{
												Key:  corev1.TLSPrivateKeyKey,
												Path: "tls.key",
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
		{
			name: "doesn't add duplicate service ownerrefs",
			mockExternal: func(mockOpClient *operatorclientmocks.MockClientInterface, fakeLister *operatorlisterfakes.FakeOperatorLister, namespace string, args args) {
				service := corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service",
						Namespace: owner.GetNamespace(),
						Labels:    map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
						OwnerReferences: []metav1.OwnerReference{
							ownerutil.NonBlockingOwner(owner),
						},
					},
					Spec: corev1.ServiceSpec{
						Ports:    args.ports,
						Selector: selector(t, "test=label").MatchLabels,
					},
				}
				portsApplyConfig := []*corev1ac.ServicePortApplyConfiguration{}
				for _, p := range args.ports {
					ac := corev1ac.ServicePort().
						WithName(p.Name).
						WithPort(p.Port).
						WithTargetPort(p.TargetPort)
					portsApplyConfig = append(portsApplyConfig, ac)
				}

				svcApplyConfig := corev1ac.Service(service.Name, service.Namespace).
					WithSpec(corev1ac.ServiceSpec().
						WithPorts(portsApplyConfig...).
						WithSelector(selector(t, "test=label").MatchLabels)).
					WithOwnerReferences(ownerutil.NonBlockingOwnerApplyConfiguration(owner)).
					WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

				mockOpClient.EXPECT().ApplyService(svcApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(&service, nil)

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
						Name:      "test-service-cert",
						Namespace: namespace,
						Annotations: map[string]string{
							OLMCAHashAnnotationKey:   caHash,
							OLMCertHashAnnotationKey: certs.PEMSHA256(certPEM),
							OLMKeyHashAnnotationKey:  certs.PEMSHA256(privPEM),
						},
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
					},
					Data: map[string][]byte{
						corev1.TLSCertKey:       certPEM,
						corev1.TLSPrivateKeyKey: privPEM,
						OLMCAPEMKey:             caPEM,
					},
					Type: corev1.SecretTypeTLS,
				}
				mockOpClient.EXPECT().UpdateSecret(secret).Return(secret, nil)

				secretRole := &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.GetName(),
						Namespace: namespace,
						Labels:    map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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
						Labels:    map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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
						Name:   service.GetName() + "-system:auth-delegator",
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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

				crbLabels := map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}
				for key, val := range ownerutil.OwnerLabel(owner, owner.GetObjectKind().GroupVersionKind().Kind) {
					crbLabels[key] = val
				}
				crbApplyConfig := rbacv1ac.ClusterRoleBinding(service.GetName() + "-system:auth-delegator").
					WithSubjects(rbacv1ac.Subject().
						WithKind("ServiceAccount").
						WithAPIGroup("").
						WithName("test-sa").
						WithNamespace(namespace)).
					WithRoleRef(rbacv1ac.RoleRef().
						WithAPIGroup("rbac.authorization.k8s.io").
						WithKind("ClusterRole").
						WithName("system:auth-delegator")).
					WithLabels(crbLabels)

				mockOpClient.EXPECT().ApplyClusterRoleBinding(crbApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(authDelegatorClusterRoleBinding, nil)

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
				authReaderRoleBinding.SetLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

				authReaderRoleBindingApplyConfig := rbacv1ac.RoleBinding(AuthReaderRoleBindingName(service.GetName()), KubeSystem).
					WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}).
					WithSubjects(rbacv1ac.Subject().
						WithKind("ServiceAccount").
						WithAPIGroup("").
						WithName(args.depSpec.Template.Spec.ServiceAccountName).
						WithNamespace(namespace)).
					WithRoleRef(rbacv1ac.RoleRef().
						WithAPIGroup("rbac.authorization.k8s.io").
						WithKind("Role").
						WithName("extension-apiserver-authentication-reader"))

				mockOpClient.EXPECT().ApplyRoleBinding(authReaderRoleBindingApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(authReaderRoleBinding, nil)
			},
			state: fakeState{
				existingService: &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: owner.GetNamespace(),
						OwnerReferences: []metav1.OwnerReference{
							ownerutil.NonBlockingOwner(owner),
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
				owner:                  owner,
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
												Key:  corev1.TLSCertKey,
												Path: "apiserver.crt",
											},
											{
												Key:  corev1.TLSPrivateKeyKey,
												Path: "apiserver.key",
											},
										},
									},
								},
							},
							{
								Name: "webhook-cert",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "test-service-cert",
										Items: []corev1.KeyToPath{
											{
												Key:  corev1.TLSCertKey,
												Path: "tls.crt",
											},
											{
												Key:  corev1.TLSPrivateKeyKey,
												Path: "tls.key",
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
		{
			name: "labels an unlabelled secret if present; creates Service and ClusterRoleBinding if not existing",
			mockExternal: func(mockOpClient *operatorclientmocks.MockClientInterface, fakeLister *operatorlisterfakes.FakeOperatorLister, namespace string, args args) {
				service := corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-service",
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
						OwnerReferences: []metav1.OwnerReference{
							ownerutil.NonBlockingOwner(&v1alpha1.ClusterServiceVersion{}),
						},
					},
					Spec: corev1.ServiceSpec{
						Ports:    args.ports,
						Selector: selector(t, "test=label").MatchLabels,
					},
				}

				portsApplyConfig := []*corev1ac.ServicePortApplyConfiguration{}
				for _, p := range args.ports {
					ac := corev1ac.ServicePort().
						WithName(p.Name).
						WithPort(p.Port).
						WithTargetPort(p.TargetPort)
					portsApplyConfig = append(portsApplyConfig, ac)
				}

				svcApplyConfig := corev1ac.Service(service.Name, service.Namespace).
					WithSpec(corev1ac.ServiceSpec().
						WithPorts(portsApplyConfig...).
						WithSelector(selector(t, "test=label").MatchLabels)).
					WithOwnerReferences(ownerutil.NonBlockingOwnerApplyConfiguration(&v1alpha1.ClusterServiceVersion{})).
					WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

				mockOpClient.EXPECT().ApplyService(svcApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(&service, nil)

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
						Name:      "test-service-cert",
						Namespace: namespace,
						Annotations: map[string]string{
							OLMCAHashAnnotationKey:   caHash,
							OLMCertHashAnnotationKey: certs.PEMSHA256(certPEM),
							OLMKeyHashAnnotationKey:  certs.PEMSHA256(privPEM),
						},
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
						OwnerReferences: []metav1.OwnerReference{
							ownerutil.NonBlockingOwner(&v1alpha1.ClusterServiceVersion{}),
						},
					},
					Data: map[string][]byte{
						corev1.TLSCertKey:       certPEM,
						corev1.TLSPrivateKeyKey: privPEM,
						OLMCAPEMKey:             caPEM,
					},
					Type: corev1.SecretTypeTLS,
				}
				// secret already exists, but without label
				mockOpClient.EXPECT().CreateSecret(secret).Return(nil, errors.NewAlreadyExists(schema.GroupResource{
					Group:    "",
					Resource: "secrets",
				}, "test-service-cert"))

				// update secret with label
				mockOpClient.EXPECT().UpdateSecret(secret).Return(secret, nil)

				secretRole := &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.GetName(),
						Namespace: namespace,
						Labels:    map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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
						Labels:    map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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
						Name:   service.GetName() + "-system:auth-delegator",
						Labels: map[string]string{OLMManagedLabelKey: OLMManagedLabelValue},
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
				crbLabels := map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}
				for key, val := range ownerutil.OwnerLabel(ownerutil.Owner(&v1alpha1.ClusterServiceVersion{}), owner.GetObjectKind().GroupVersionKind().Kind) {
					crbLabels[key] = val
				}
				crbApplyConfig := rbacv1ac.ClusterRoleBinding(AuthDelegatorClusterRoleBindingName(service.GetName())).
					WithSubjects(rbacv1ac.Subject().WithKind("ServiceAccount").
						WithAPIGroup("").
						WithName("test-sa").
						WithNamespace(namespace)).
					WithRoleRef(rbacv1ac.RoleRef().
						WithAPIGroup("rbac.authorization.k8s.io").
						WithKind("ClusterRole").
						WithName("system:auth-delegator")).
					WithLabels(crbLabels)

				mockOpClient.EXPECT().ApplyClusterRoleBinding(crbApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(authDelegatorClusterRoleBinding, nil)

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
				authReaderRoleBinding.SetLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue})

				authReaderRoleBindingApplyConfig := rbacv1ac.RoleBinding(AuthReaderRoleBindingName(service.GetName()), KubeSystem).
					WithLabels(map[string]string{OLMManagedLabelKey: OLMManagedLabelValue}).
					WithSubjects(rbacv1ac.Subject().
						WithKind("ServiceAccount").
						WithAPIGroup("").
						WithName(args.depSpec.Template.Spec.ServiceAccountName).
						WithNamespace(namespace)).
					WithRoleRef(rbacv1ac.RoleRef().
						WithAPIGroup("rbac.authorization.k8s.io").
						WithKind("Role").
						WithName("extension-apiserver-authentication-reader"))

				mockOpClient.EXPECT().ApplyRoleBinding(authReaderRoleBindingApplyConfig, metav1.ApplyOptions{Force: true, FieldManager: "olm.install"}).Return(authReaderRoleBinding, nil)
			},
			state: fakeState{
				existingService: nil,
				// unlabelled secret won't be in cache
				getSecretError: errors.NewNotFound(schema.GroupResource{
					Group:    "",
					Resource: "Secret",
				}, "nope"),
				existingRole: &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{},
				},
				existingRoleBinding: &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{},
				},
				existingClusterRoleBinding: nil,
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
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"foo": "bar",
							},
						},
					},
				},
			},
			want: &appsv1.DeploymentSpec{
				Selector: selector(t, "test=label"),
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"foo":                  "bar",
							OLMCAHashAnnotationKey: caHash},
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
												Key:  corev1.TLSCertKey,
												Path: "apiserver.crt",
											},
											{
												Key:  corev1.TLSPrivateKeyKey,
												Path: "apiserver.key",
											},
										},
									},
								},
							},
							{
								Name: "webhook-cert",
								VolumeSource: corev1.VolumeSource{
									Secret: &corev1.SecretVolumeSource{
										SecretName: "test-service-cert",
										Items: []corev1.KeyToPath{
											{
												Key:  corev1.TLSCertKey,
												Path: "tls.crt",
											},
											{
												Key:  corev1.TLSPrivateKeyKey,
												Path: "tls.key",
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
			got, _, err := i.installCertRequirementsForDeployment(tt.args.deploymentName, tt.args.ca, tt.args.rotateAt, tt.args.depSpec, tt.args.ports)
			if (err != nil) != tt.wantErr {
				t.Errorf("installCertRequirementsForDeployment() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("installCertRequirementsForDeployment() \n got  = %v \n want = %v\n diff=%s\n", got, tt.want, cmp.Diff(got, tt.want))
			}
		})
	}
}
