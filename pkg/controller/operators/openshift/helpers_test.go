package openshift

import (
	"context"
	"fmt"
	"testing"

	semver "github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

func TestVersionsMatch(t *testing.T) {
	type in struct {
		a, b []configv1.OperandVersion
	}
	for _, tt := range []struct {
		description string
		in          in
		expect      bool
	}{
		{
			description: "Different/Nil",
			in: in{
				a: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
				},
				b: nil,
			},
			expect: false,
		},
		{
			description: "Different/Names",
			in: in{
				a: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
				},
				b: []configv1.OperandVersion{
					{Name: "yutani", Version: "1.0.0"},
				},
			},
			expect: false,
		},
		{
			description: "Different/Versions",
			in: in{
				a: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
				},
				b: []configv1.OperandVersion{
					{Name: "weyland", Version: "2.0.0"},
				},
			},
			expect: false,
		},
		{
			description: "Different/Lengths",
			in: in{
				a: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
				},
				b: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
					{Name: "yutani", Version: "1.0.0"},
				},
			},
			expect: false,
		},
		{
			description: "Different/Elements",
			in: in{
				a: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
					{Name: "weyland", Version: "1.0.0"},
					{Name: "weyland", Version: "1.0.0"},
					{Name: "yutani", Version: "1.0.0"},
				},
				b: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
					{Name: "weyland", Version: "1.0.0"},
					{Name: "yutani", Version: "1.0.0"},
					{Name: "yutani", Version: "1.0.0"},
				},
			},
			expect: false,
		},
		{
			description: "Same/Nil",
			in: in{
				a: nil,
				b: nil,
			},
			expect: true,
		},
		{
			description: "Same/Empty",
			in: in{
				a: []configv1.OperandVersion{},
				b: []configv1.OperandVersion{},
			},
			expect: true,
		},
		{
			description: "Same/Empty/Nil",
			in: in{
				a: []configv1.OperandVersion{},
				b: nil,
			},
			expect: true,
		},
		{
			description: "Same",
			in: in{
				a: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
					{Name: "yutani", Version: "1.0.0"},
				},
				b: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
					{Name: "yutani", Version: "1.0.0"},
				},
			},
			expect: true,
		},
		{
			description: "Same/Unordered",
			in: in{
				a: []configv1.OperandVersion{
					{Name: "weyland", Version: "1.0.0"},
					{Name: "yutani", Version: "1.0.0"},
				},
				b: []configv1.OperandVersion{
					{Name: "yutani", Version: "1.0.0"},
					{Name: "weyland", Version: "1.0.0"},
				},
			},
			expect: true,
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			require.Equal(t, tt.expect, versionsMatch(tt.in.a, tt.in.b))
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
		cv          configv1.ClusterVersion
		in          skews
		expect      expect
	}{
		{
			description: "Compatible",
			cv: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Update{
						Version: "1.0.0",
					},
				},
			},
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
			cv: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Update{
						Version: "1.0.0",
					},
				},
			},
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
			cv: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Update{
						Version: "1.0.0",
					},
				},
			},
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
					maxOpenShiftVersion: "1.0",
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
					{
						name:                "chestnut",
						namespace:           "default",
						maxOpenShiftVersion: "1.0.0",
					},
				},
			},
		},
		{
			description: "Mixed/BadVersion",
			cv: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Update{
						Version: "1.0.0",
					},
				},
			},
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
		{
			description: "Compatible/EmptyVersion",
			cv: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Update{
						Version: "",
					},
				},
			},
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
				err:          false,
				incompatible: nil,
			},
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			objs := []client.Object{tt.cv.DeepCopy()}

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
		version, err := semver.ParseTolerant(s)
		if err != nil {
			panic(fmt.Sprintf("bad version given for test case: %s", err))
		}
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
			in:          []string{`""`},
			expect: expect{
				err: false,
				max: nil,
			},
		},
		{
			description: "Nothing/Mixed",
			in: []string{
				`""`,
				`"1.0.0"`,
			},
			expect: expect{
				err: false,
				max: mustParse("1.0.0"),
			},
		},
		{
			description: "Garbage",
			in:          []string{`"bad_version"`},
			expect: expect{
				err: true,
				max: nil,
			},
		},
		{
			description: "Garbage/Mixed",
			in: []string{
				`"bad_version"`,
				`"1.0.0"`,
			},
			expect: expect{
				err: true,
				max: nil,
			},
		},
		{
			description: "Single",
			in:          []string{`"1.0.0"`},
			expect: expect{
				err: false,
				max: mustParse("1.0.0"),
			},
		},
		{
			description: "Multiple",
			in: []string{
				`"1.0.0"`,
				`"2.0.0"`,
			},
			expect: expect{
				err: false,
				max: mustParse("2.0.0"),
			},
		},
		{
			description: "Duplicates",
			in: []string{
				`"1.0.0"`,
				`"1.0.0"`,
			},
			expect: expect{
				err: false,
				max: mustParse("1.0.0"),
			},
		},
		{
			description: "Duplicates/NonMax",
			in: []string{
				`"1.0.0"`,
				`"1.0.0"`,
				`"2.0.0"`,
			},
			expect: expect{
				err: false,
				max: mustParse("2.0.0"),
			},
		},
		{
			description: "Ambiguous",
			in: []string{
				`"1.0.0"`,
				`"1.0.0+1"`,
			},
			expect: expect{
				err: true,
				max: nil,
			},
		},
		{
			// Ensure unquoted short strings are accepted; e.g. X.Y
			description: "Unquoted/Short",
			in:          []string{"4.8"},
			expect: expect{
				err: false,
				max: mustParse("4.8"),
			},
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			var properties []*api.Property
			for _, max := range tt.in {
				properties = append(properties, &api.Property{
					Type:  MaxOpenShiftVersionProperty,
					Value: max,
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

func TestNotCopiedSelector(t *testing.T) {
	for _, tc := range []struct {
		Labels  labels.Set
		Matches bool
	}{
		{
			Labels:  labels.Set{operatorsv1alpha1.CopiedLabelKey: ""},
			Matches: false,
		},
		{
			Labels:  labels.Set{},
			Matches: true,
		},
	} {
		t.Run(tc.Labels.String(), func(t *testing.T) {
			selector, err := notCopiedSelector()
			require.NoError(t, err)
			require.Equal(t, tc.Matches, selector.Matches(tc.Labels))
		})
	}
}
