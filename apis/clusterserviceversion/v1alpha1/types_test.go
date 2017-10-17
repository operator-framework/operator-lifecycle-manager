package v1alpha1

import (
	"testing"
	"github.com/stretchr/testify/require"
)

func TestGetAllCrds(t *testing.T) {
	crd := CustomResourceDefinitions{
		Owned: []CRDDescription{{Name: "first"}, {Name: "second"}, {Name: "second"}, {Name: "second"}},
		Required: []CRDDescription{{Name: "third"}, {Name: "second"}},
	}

	allCrds := crd.GetAllCrds()
	require.NotNil(t, allCrds)
	require.Equal(t, 3, len(allCrds))
	require.Contains(t, allCrds, CRDDescription{Name: "first"})
	require.Contains(t, allCrds, CRDDescription{Name: "second"})
	require.Contains(t, allCrds, CRDDescription{Name: "third"})
}
