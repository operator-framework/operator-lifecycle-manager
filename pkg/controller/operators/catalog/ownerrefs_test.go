package catalog

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDedupe(t *testing.T) {
	yes := true
	refs := []metav1.OwnerReference{
		{
			APIVersion:         "apiversion",
			Kind:               "kind",
			Name:               "name",
			UID:                "uid",
			Controller:         &yes,
			BlockOwnerDeletion: &yes,
		},
		{
			APIVersion:         "apiversion",
			Kind:               "kind",
			Name:               "name",
			UID:                "uid",
			Controller:         &yes,
			BlockOwnerDeletion: &yes,
		},
		{
			APIVersion:         "apiversion",
			Kind:               "kind",
			Name:               "name",
			UID:                "uid",
			Controller:         &yes,
			BlockOwnerDeletion: &yes,
		},
	}
	deduped := deduplicateOwnerReferences(refs)
	t.Logf("got %d deduped from %d", len(deduped), len(refs))
	if len(deduped) == len(refs) {
		t.Errorf("didn't dedupe: %#v", deduped)
	}
}
