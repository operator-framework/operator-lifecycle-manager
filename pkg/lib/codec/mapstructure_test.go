package codec

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMetaTimeHookFunc(t *testing.T) {
	type args struct {
		fromType, toType reflect.Type
		data             interface{}
	}
	type expected struct {
		converted interface{}
		err       error
	}
	tests := []struct {
		description string
		args        args
		expected    expected
	}{
		{
			description: "TimeToNil/Passthrough",
			args: args{
				fromType: reflect.TypeOf(metav1.Time{}),
				toType:   reflect.TypeOf(nil),
				data:     &metav1.Time{},
			},
			expected: expected{
				converted: &metav1.Time{},
			},
		},
		{
			description: "TimeToUnsupported/Passthrough",
			args: args{
				fromType: reflect.TypeOf(metav1.Time{}),
				toType:   reflect.TypeOf(0),
				data:     &metav1.Time{},
			},
			expected: expected{
				converted: &metav1.Time{},
			},
		},
		{
			description: "TimeToTime/Passthrough",
			args: args{
				fromType: reflect.TypeOf(metav1.Time{}),
				toType:   reflect.TypeOf(metav1.Time{}),
				data:     &metav1.Time{},
			},
			expected: expected{
				converted: &metav1.Time{},
			},
		},
		{
			description: "UnsupportedToTime/Passthrough",
			args: args{
				fromType: reflect.TypeOf(0),
				toType:   reflect.TypeOf(metav1.Time{}),
				data:     0,
			},
			expected: expected{
				converted: 0,
			},
		},
		{
			description: "InvalidStringToTime/Errors",
			args: args{
				fromType: reflect.TypeOf(""),
				toType:   reflect.TypeOf(metav1.Time{}),
				data:     "Not a valid RFC3339 string!",
			},
			expected: expected{
				err: func() error {
					_, err := time.Parse(time.RFC3339, "Not a valid RFC3339 string!")
					return err
				}(),
			},
		},
		{
			description: "StringToTime",
			args: args{
				fromType: reflect.TypeOf(""),
				toType:   reflect.TypeOf(metav1.Time{}),
				data:     "2002-10-02T10:00:00-05:00",
			},
			expected: expected{
				converted: func() metav1.Time {
					tm, err := time.Parse(time.RFC3339, "2002-10-02T10:00:00-05:00")
					require.NoError(t, err)
					return metav1.NewTime(tm)
				}(),
			},
		},
		{
			description: "Float64ToTime",
			args: args{
				fromType: reflect.TypeOf(float64(0.0)),
				toType:   reflect.TypeOf(metav1.Time{}),
				data:     float64(0.0),
			},
			expected: expected{
				converted: metav1.NewTime(time.Unix(0, 0)),
			},
		},
		{
			description: "Int64ToTime",
			args: args{
				fromType: reflect.TypeOf(int64(0)),
				toType:   reflect.TypeOf(metav1.Time{}),
				data:     int64(0),
			},
			expected: expected{
				converted: metav1.NewTime(time.Unix(0, 0)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			converted, err := metaTimeHookFunc(tt.args.fromType, tt.args.toType, tt.args.data)
			require.Equal(t, tt.expected.err, err)
			require.EqualValues(t, tt.expected.converted, converted)
		})
	}
}
