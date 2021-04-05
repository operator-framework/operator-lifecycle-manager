package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMergedOwnerReferences(t *testing.T) {
	var (
		True  bool = true
		False bool = false
	)

	for _, tc := range []struct {
		Name string
		In   [][]metav1.OwnerReference
		Out  []metav1.OwnerReference
	}{
		{
			Name: "empty",
		},
		{
			Name: "different uid",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "y",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "y",
				},
			},
		},
		{
			Name: "different controller",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &True,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &True,
						UID:                "x",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &True,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &False,
					BlockOwnerDeletion: &True,
					UID:                "x",
				},
			},
		},
		{
			Name: "add owner without uid",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c-1",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c-2",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c-1",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "x",
				},
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c-2",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "",
				},
			},
		},
		{
			Name: "duplicates combined",
			In: [][]metav1.OwnerReference{
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
				{
					{
						APIVersion:         "a",
						Kind:               "b",
						Name:               "c",
						Controller:         &False,
						BlockOwnerDeletion: &False,
						UID:                "x",
					},
				},
			},
			Out: []metav1.OwnerReference{
				{
					APIVersion:         "a",
					Kind:               "b",
					Name:               "c",
					Controller:         &False,
					BlockOwnerDeletion: &False,
					UID:                "x",
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.Out, mergedOwnerReferences(tc.In...))
		})
	}
}
