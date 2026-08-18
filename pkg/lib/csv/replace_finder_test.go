package csv

import (
	"testing"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
)

func newCSV(name, replaces string) *v1alpha1.ClusterServiceVersion {
	return &v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec:       v1alpha1.ClusterServiceVersionSpec{Replaces: replaces},
	}
}

func setOf(csvs ...*v1alpha1.ClusterServiceVersion) map[string]*v1alpha1.ClusterServiceVersion {
	set := map[string]*v1alpha1.ClusterServiceVersion{}
	for _, csv := range csvs {
		set[csv.GetName()] = csv
	}
	return set
}

func TestIsBeingReplacedIgnoresSelf(t *testing.T) {
	finder := NewReplaceFinder(logrus.New(), fake.NewSimpleClientset())
	self := newCSV("a", "a")
	if got := finder.IsBeingReplaced(self, setOf(self)); got != nil {
		t.Fatalf("self-replacing CSV reported as being replaced by %q", got.GetName())
	}
}

func TestIsReplacingIgnoresSelf(t *testing.T) {
	self := newCSV("a", "a")
	finder := NewReplaceFinder(logrus.New(), fake.NewSimpleClientset(self))
	if got := finder.IsReplacing(self); got != nil {
		t.Fatalf("self-replacing CSV reported as replacing %q", got.GetName())
	}
}

// Regression test for OCPBUGS-23954: these calls looped forever before the
// cycle guard.
func TestGetFinalCSVInReplacingTerminates(t *testing.T) {
	finder := NewReplaceFinder(logrus.New(), fake.NewSimpleClientset())

	// self-loop: a replaces a
	self := newCSV("a", "a")
	if got := finder.GetFinalCSVInReplacing(self, setOf(self)); got != nil {
		t.Fatalf("self-loop: expected nil, got %q", got.GetName())
	}

	// two-CSV cycle: a replaces b, b replaces a
	a := newCSV("a", "b")
	b := newCSV("b", "a")
	got := finder.GetFinalCSVInReplacing(a, setOf(a, b))
	if got == nil || got.GetName() != "b" {
		t.Fatalf("two-CSV cycle: expected b, got %v", got)
	}

	// linear chain: c replaces b replaces a, walk from a ends at c
	a = newCSV("a", "")
	b = newCSV("b", "a")
	c := newCSV("c", "b")
	got = finder.GetFinalCSVInReplacing(a, setOf(a, b, c))
	if got == nil || got.GetName() != "c" {
		t.Fatalf("linear chain: expected c, got %v", got)
	}
}
