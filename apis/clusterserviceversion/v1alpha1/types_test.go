package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAllCrds(t *testing.T) {
	crd := CustomResourceDefinitions{
		Owned:    []CRDDescription{{Name: "first"}, {Name: "second"}, {Name: "second"}, {Name: "second"}},
		Required: []CRDDescription{{Name: "third"}, {Name: "second"}},
	}

	allCrds := crd.GetAllCrds()
	require.NotNil(t, allCrds)
	require.Equal(t, 3, len(allCrds))
	require.Contains(t, allCrds, CRDDescription{Name: "first"})
	require.Contains(t, allCrds, CRDDescription{Name: "second"})
	require.Contains(t, allCrds, CRDDescription{Name: "third"})
}

func TestOwnsCRD(t *testing.T) {
	var table = []struct {
		ownedCRDNames []string
		crdName       string
		expected      bool
	}{
		{nil, "", false},
		{nil, "querty", false},
		{[]string{}, "", false},
		{[]string{}, "querty", false},
		{[]string{"owned"}, "owned", true},
		{[]string{"owned"}, "notOwned", false},
		{[]string{"first", "second"}, "first", true},
		{[]string{"first", "second"}, "second", true},
		{[]string{"first", "second"}, "third", false},
	}

	for _, tt := range table {
		// Build a list of CRDDescription used in the CSV.
		var ownedDescriptions []CRDDescription
		for _, crdName := range tt.ownedCRDNames {
			ownedDescriptions = append(ownedDescriptions, CRDDescription{
				Name: crdName,
			})
		}

		// Create a blank CSV with the owned descriptions.
		csv := ClusterServiceVersion{
			Spec: ClusterServiceVersionSpec{
				CustomResourceDefinitions: CustomResourceDefinitions{
					Owned: ownedDescriptions,
				},
			},
		}

		// Call OwnsCRD and ensure the result is as expected.
		require.Equal(t, tt.expected, csv.OwnsCRD(tt.crdName))
	}
}
