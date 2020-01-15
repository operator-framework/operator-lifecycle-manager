package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetDefaultOsArchLabels(t *testing.T) {
	type args struct {
		labels map[string]string
	}
	tests := []struct {
		name     string
		args     args
		expected map[string]string
	}{
		{
			name: "NoneSet",
			args: args{labels: map[string]string{}},
			expected: map[string]string{
				DefaultArchLabel: Supported,
				DefaultOsLabel:   Supported,
			},
		},
		{
			name: "OthersPreserved",
			args: args{labels: map[string]string{"other": "label"}},
			expected: map[string]string{
				"other":          "label",
				DefaultArchLabel: Supported,
				DefaultOsLabel:   Supported,
			},
		},
		{
			name: "OsSet",
			args: args{labels: map[string]string{OsLabelPrefix + ".windows": Supported}},
			expected: map[string]string{
				DefaultArchLabel:           Supported,
				OsLabelPrefix + ".windows": Supported,
			},
		},
		{
			name: "ArchSet",
			args: args{labels: map[string]string{ArchLabelPrefix + ".arm": Supported}},
			expected: map[string]string{
				ArchLabelPrefix + ".arm": Supported,
				DefaultOsLabel:           Supported,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := tt.args.labels
			setDefaultOsArchLabels(labels)
			require.Equal(t, tt.expected, labels)
		})
	}
}
