package v1alpha1

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAllCRDDescriptions(t *testing.T) {
	var table = []struct {
		owned    []string
		required []string
		expected []string
	}{
		{nil, nil, nil},
		{[]string{}, []string{}, []string{}},
		{[]string{"owned"}, []string{}, []string{"owned"}},
		{[]string{}, []string{"required"}, []string{"required"}},
		{[]string{"owned"}, []string{"required"}, []string{"owned", "required"}},
		{[]string{"first", "second"}, []string{"first", "second"}, []string{"first", "second"}},
		{[]string{"first", "second", "third"}, []string{"second", "third", "fourth"}, []string{"first", "second", "third", "fourth"}},
	}

	for _, tt := range table {
		// Build a list of owned CRDDescription used in the CSV.
		ownedDescriptions := make([]CRDDescription, 0)
		for _, crdName := range tt.owned {
			ownedDescriptions = append(ownedDescriptions, CRDDescription{
				Name: crdName,
			})
		}

		// Build a list of owned CRDDescription used in the CSV.
		requiredDescriptions := make([]CRDDescription, 0)
		for _, crdName := range tt.required {
			requiredDescriptions = append(requiredDescriptions, CRDDescription{
				Name: crdName,
			})
		}

		// Build a list of expected CRDDescriptions.
		expectedDescriptions := make([]CRDDescription, 0)
		sort.StringSlice(tt.expected).Sort()
		for _, expectedName := range tt.expected {
			expectedDescriptions = append(expectedDescriptions, CRDDescription{
				Name: expectedName,
			})
		}

		// Create a blank CSV with the owned descriptions.
		csv := ClusterServiceVersion{
			Spec: ClusterServiceVersionSpec{
				CustomResourceDefinitions: CustomResourceDefinitions{
					Owned:    ownedDescriptions,
					Required: requiredDescriptions,
				},
			},
		}

		// Call GetAllCRDDescriptions and ensure the result is as expected.
		require.Equal(t, expectedDescriptions, csv.GetAllCRDDescriptions())
	}
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
