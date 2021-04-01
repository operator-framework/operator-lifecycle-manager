package openshift

import (
	"context"
	"testing"

	semver "github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-registry/pkg/api"
)

func TestConditionsEqual(t *testing.T) {
	type args struct {
		a, b *configv1.ClusterOperatorStatusCondition
	}
	for _, tt := range []struct {
		description string
		args        args
		expect      bool
	}{
		{
			description: "Nil/Both",
			expect:      true,
		},
		{
			description: "Nil/A",
			args: args{
				b: &configv1.ClusterOperatorStatusCondition{},
			},
			expect: false,
		},
		{
			description: "Nil/B",
			args: args{
				a: &configv1.ClusterOperatorStatusCondition{},
			},
			expect: false,
		},
		{
			description: "Same",
			args: args{
				a: &configv1.ClusterOperatorStatusCondition{},
				b: &configv1.ClusterOperatorStatusCondition{},
			},
			expect: true,
		},
		{
			description: "Different/LastTransitionTime",
			args: args{
				a: &configv1.ClusterOperatorStatusCondition{
					LastTransitionTime: metav1.Now(),
				},
				b: &configv1.ClusterOperatorStatusCondition{},
			},
			expect: true,
		},
		{
			description: "Different/Status",
			args: args{
				a: &configv1.ClusterOperatorStatusCondition{
					Status: configv1.ConditionTrue,
				},
				b: &configv1.ClusterOperatorStatusCondition{},
			},
			expect: false,
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			require.Equal(t, tt.expect, conditionsEqual(tt.args.a, tt.args.b))
		})
	}

}

func TestIncompatibleOperators(t *testing.T) {
	type expect struct {
		err          bool
		incompatible skews
	}
	for _, tt := range []struct {
		description string
		in          skews
		expect      expect
	}{
		{
			description: "Compatible",
			in: skews{
				{
					name:                "almond",
					namespace:           "default",
					maxOpenShiftVersion: "1.1.0",
				},
				{
					name:                "beech",
					namespace:           "default",
					maxOpenShiftVersion: "1.1.0+build",
				},
				{
					name:                "chestnut",
					namespace:           "default",
					maxOpenShiftVersion: "2.0.0",
				},
			},
			expect: expect{
				err:          false,
				incompatible: nil,
			},
		},
		{
			description: "Incompatible",
			in: skews{
				{
					name:                "almond",
					namespace:           "default",
					maxOpenShiftVersion: "1.0.0",
				},
				{
					name:                "beech",
					namespace:           "default",
					maxOpenShiftVersion: "1.0.0+build",
				},
				{
					name:                "chestnut",
					namespace:           "default",
					maxOpenShiftVersion: "1.1.0-pre",
				},
				{
					name:                "drupe",
					namespace:           "default",
					maxOpenShiftVersion: "0.1.0",
				},
			},
			expect: expect{
				err: false,
				incompatible: skews{
					{
						name:                "almond",
						namespace:           "default",
						maxOpenShiftVersion: "1.0.0",
					},
					{
						name:                "beech",
						namespace:           "default",
						maxOpenShiftVersion: "1.0.0+build",
					},
					{
						name:                "chestnut",
						namespace:           "default",
						maxOpenShiftVersion: "1.1.0-pre",
					},
					{
						name:                "drupe",
						namespace:           "default",
						maxOpenShiftVersion: "0.1.0",
					},
				},
			},
		},
		{
			description: "Mixed",
			in: skews{
				{
					name:                "almond",
					namespace:           "default",
					maxOpenShiftVersion: "1.1.0",
				},
				{
					name:                "beech",
					namespace:           "default",
					maxOpenShiftVersion: "1.0.0",
				},
			},
			expect: expect{
				err: false,
				incompatible: skews{
					{
						name:                "beech",
						namespace:           "default",
						maxOpenShiftVersion: "1.0.0",
					},
				},
			},
		},
		{
			description: "Mixed/BadVersion",
			in: skews{
				{
					name:                "almond",
					namespace:           "default",
					maxOpenShiftVersion: "1.1.0",
				},
				{
					name:                "beech",
					namespace:           "default",
					maxOpenShiftVersion: "1.0.0",
				},
				{
					name:                "chestnut",
					namespace:           "default",
					maxOpenShiftVersion: "bad_version",
				},
			},
			expect: expect{
				err: true,
				incompatible: skews{
					{
						name:                "beech",
						namespace:           "default",
						maxOpenShiftVersion: "1.0.0",
					},
				},
			},
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			cv := &configv1.ClusterVersion{}
			cv.SetName("version")
			cv.Status.Desired.Version = "1.0.0"
			objs := []client.Object{cv}

			for _, s := range tt.in {
				csv := &operatorsv1alpha1.ClusterServiceVersion{}
				csv.SetName(s.name)
				csv.SetNamespace(s.namespace)

				maxProperty := &api.Property{
					Type:  MaxOpenShiftVersionProperty,
					Value: `"` + s.maxOpenShiftVersion + `"`, // Wrap in quotes so we don't break property marshaling
				}
				value, err := projection.PropertiesAnnotationFromPropertyList([]*api.Property{maxProperty})
				require.NoError(t, err)

				csv.SetAnnotations(map[string]string{
					projection.PropertiesAnnotationKey: value,
				})

				objs = append(objs, csv)
			}

			scheme := runtime.NewScheme()
			require.NoError(t, AddToScheme(scheme))

			fcli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			incompatible, err := incompatibleOperators(context.Background(), fcli)
			if tt.expect.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.ElementsMatch(t, tt.expect.incompatible, incompatible)
		})
	}
}

func TestMaxOpenShiftVersion(t *testing.T) {
	mustParse := func(s string) *semver.Version {
		version := semver.MustParse(s)
		return &version
	}

	type expect struct {
		err bool
		max *semver.Version
	}
	for _, tt := range []struct {
		description string
		in          []string
		expect      expect
	}{
		{
			description: "None",
			expect: expect{
				err: false,
				max: nil,
			},
		},
		{
			description: "Nothing",
			in:          []string{""},
			expect: expect{
				err: false,
				max: nil,
			},
		},
		{
			description: "Nothing/Mixed",
			in: []string{
				"",
				"1.0.0",
			},
			expect: expect{
				err: false,
				max: mustParse("1.0.0"),
			},
		},
		{
			description: "Garbage",
			in:          []string{"bad_version"},
			expect: expect{
				err: true,
				max: nil,
			},
		},
		{
			description: "Garbage/Mixed",
			in: []string{
				"bad_version",
				"1.0.0",
			},
			expect: expect{
				err: true,
				max: nil,
			},
		},
		{
			description: "Single",
			in:          []string{"1.0.0"},
			expect: expect{
				err: false,
				max: mustParse("1.0.0"),
			},
		},
		{
			description: "Multiple",
			in: []string{
				"1.0.0",
				"2.0.0",
			},
			expect: expect{
				err: false,
				max: mustParse("2.0.0"),
			},
		},
		{
			description: "Duplicates",
			in: []string{
				"1.0.0",
				"1.0.0",
			},
			expect: expect{
				err: false,
				max: mustParse("1.0.0"),
			},
		},
		{
			description: "Duplicates/NonMax",
			in: []string{
				"1.0.0",
				"1.0.0",
				"2.0.0",
			},
			expect: expect{
				err: false,
				max: mustParse("2.0.0"),
			},
		},
		{
			description: "Ambiguous",
			in: []string{
				"1.0.0",
				"1.0.0+1",
			},
			expect: expect{
				err: true,
				max: nil,
			},
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			var properties []*api.Property
			for _, max := range tt.in {
				properties = append(properties, &api.Property{
					Type:  MaxOpenShiftVersionProperty,
					Value: `"` + max + `"`, // Wrap in quotes so we don't break property marshaling
				})
			}

			value, err := projection.PropertiesAnnotationFromPropertyList(properties)
			require.NoError(t, err)

			csv := &operatorsv1alpha1.ClusterServiceVersion{}
			csv.SetAnnotations(map[string]string{
				projection.PropertiesAnnotationKey: value,
			})

			max, err := maxOpenShiftVersion(csv)
			if tt.expect.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.expect.max, max)
		})
	}
}
