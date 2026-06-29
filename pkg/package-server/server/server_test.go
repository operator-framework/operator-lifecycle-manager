package server

import (
	"context"
	"testing"

	apiconfigv1 "github.com/openshift/api/config/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	fakediscovery "k8s.io/client-go/discovery/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// clusterAPIServer returns a minimal APIServer singleton with the given TLS profile.
func clusterAPIServer(profile *apiconfigv1.TLSSecurityProfile) *apiconfigv1.APIServer {
	return &apiconfigv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       apiconfigv1.APIServerSpec{TLSSecurityProfile: profile},
	}
}

func newServing() *genericoptions.SecureServingOptionsWithLoopback {
	return genericoptions.NewSecureServingOptions().WithLoopback()
}

// nonOpenShiftDiscovery returns a fake discovery that advertises only core k8s
// groups — no config.openshift.io — simulating a vanilla Kubernetes cluster.
func nonOpenShiftDiscovery() *fakediscovery.FakeDiscovery {
	k8sClient := k8sfake.NewSimpleClientset()
	disc := k8sClient.Discovery().(*fakediscovery.FakeDiscovery)
	// Set a non-empty resource list so ServerGroups doesn't return an empty
	// list (which ServerSupportsVersion treats as "all supported").
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "v1"},
	}
	return disc
}

// TestApplyClusterTLSProfileWithClients_NonOpenShift verifies that the function
// is a no-op when the OpenShift config API is not present (vanilla Kubernetes).
func TestApplyClusterTLSProfileWithClients_NonOpenShift(t *testing.T) {
	cfgClient := configfake.NewSimpleClientset()
	serving := newServing()

	err := applyClusterTLSProfileWithClients(context.Background(), nonOpenShiftDiscovery(), cfgClient, serving)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if serving.MinTLSVersion != "" {
		t.Errorf("expected MinTLSVersion to be unset, got %q", serving.MinTLSVersion)
	}
	if len(serving.CipherSuites) != 0 {
		t.Errorf("expected CipherSuites to be unset, got %v", serving.CipherSuites)
	}
}

// TestApplyClusterTLSProfileWithClients_IntermediateProfile verifies that the
// Intermediate TLS profile populates MinTLSVersion and CipherSuites.
func TestApplyClusterTLSProfileWithClients_IntermediateProfile(t *testing.T) {
	apiServer := clusterAPIServer(&apiconfigv1.TLSSecurityProfile{
		Type: apiconfigv1.TLSProfileIntermediateType,
	})
	cfgClient := configfake.NewSimpleClientset(apiServer)
	serving := newServing()

	err := applyClusterTLSProfileWithClients(context.Background(), cfgClient.Discovery(), cfgClient, serving)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if serving.MinTLSVersion == "" {
		t.Error("expected MinTLSVersion to be set for Intermediate profile")
	}
	if len(serving.CipherSuites) == 0 {
		t.Error("expected CipherSuites to be set for Intermediate profile")
	}
}

// TestApplyClusterTLSProfileWithClients_ModernProfile verifies that the Modern
// profile sets MinTLSVersion to VersionTLS13.
func TestApplyClusterTLSProfileWithClients_ModernProfile(t *testing.T) {
	apiServer := clusterAPIServer(&apiconfigv1.TLSSecurityProfile{
		Type: apiconfigv1.TLSProfileModernType,
	})
	cfgClient := configfake.NewSimpleClientset(apiServer)
	serving := newServing()

	err := applyClusterTLSProfileWithClients(context.Background(), cfgClient.Discovery(), cfgClient, serving)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if serving.MinTLSVersion != "VersionTLS13" {
		t.Errorf("expected MinTLSVersion=VersionTLS13 for Modern profile, got %q", serving.MinTLSVersion)
	}
}

// TestApplyClusterTLSProfileWithClients_FlagsTakePrecedence verifies that
// explicitly set flags are not overwritten by the cluster profile.
func TestApplyClusterTLSProfileWithClients_FlagsTakePrecedence(t *testing.T) {
	apiServer := clusterAPIServer(&apiconfigv1.TLSSecurityProfile{
		Type: apiconfigv1.TLSProfileModernType,
	})
	cfgClient := configfake.NewSimpleClientset(apiServer)
	serving := newServing()
	// Simulate user-supplied flags.
	serving.MinTLSVersion = "VersionTLS12"
	serving.CipherSuites = []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}

	err := applyClusterTLSProfileWithClients(context.Background(), cfgClient.Discovery(), cfgClient, serving)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if serving.MinTLSVersion != "VersionTLS12" {
		t.Errorf("user-supplied MinTLSVersion should not be overwritten, got %q", serving.MinTLSVersion)
	}
	if len(serving.CipherSuites) != 1 || serving.CipherSuites[0] != "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256" {
		t.Errorf("user-supplied CipherSuites should not be overwritten, got %v", serving.CipherSuites)
	}
}

// TestApplyClusterTLSProfileWithClients_MissingAPIServerCR verifies that a
// missing singleton APIServer CR propagates as an error.
func TestApplyClusterTLSProfileWithClients_MissingAPIServerCR(t *testing.T) {
	// config client advertises the API group but has no APIServer object
	cfgClient := configfake.NewSimpleClientset()
	serving := newServing()

	err := applyClusterTLSProfileWithClients(context.Background(), cfgClient.Discovery(), cfgClient, serving)
	if err == nil {
		t.Fatal("expected an error when the APIServer CR is missing, got nil")
	}
}

// TestApplyClusterTLSProfileWithClients_ErrorLeavesServingUnchanged is a
// regression test for the warn-and-continue startup behavior introduced to
// prevent crash-loops when the API server is not yet reachable.
//
// It verifies that when applyClusterTLSProfileWithClients returns an error the
// SecureServingOptions are left in their original state, so the caller can
// safely ignore the error and start with secure TLS defaults.
func TestApplyClusterTLSProfileWithClients_ErrorLeavesServingUnchanged(t *testing.T) {
	cfgClient := configfake.NewSimpleClientset() // no APIServer CR → returns error
	serving := newServing()

	err := applyClusterTLSProfileWithClients(context.Background(), cfgClient.Discovery(), cfgClient, serving)
	if err == nil {
		t.Fatal("expected an error to simulate API unavailability, got nil")
	}

	// Serving options must be unchanged so that starting with defaults is safe.
	if serving.MinTLSVersion != "" {
		t.Errorf("MinTLSVersion should be unset after error, got %q", serving.MinTLSVersion)
	}
	if len(serving.CipherSuites) != 0 {
		t.Errorf("CipherSuites should be unset after error, got %v", serving.CipherSuites)
	}
}
