package comparison

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type equalitorArgs struct {
	a interface{}
	b interface{}
}
type equalitorWants struct {
	equal bool
}
type equalitorTest struct {
	description string
	args        equalitorArgs
	wants       equalitorWants
}

var (
	standardSuite = []equalitorTest{
		{
			description: "EmptyStructs/True",
			args: equalitorArgs{
				a: struct{}{},
				b: struct{}{},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
		{
			description: "Structs/Exported/True",
			args: equalitorArgs{
				a: struct {
					Animal string
				}{
					Animal: "hippo",
				},
				b: struct {
					Animal string
				}{
					Animal: "hippo",
				},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
		{
			description: "Structs/Exported/False",
			args: equalitorArgs{
				a: struct {
					Animal string
				}{
					Animal: "hippo",
				},
				b: struct {
					Animal string
				}{
					Animal: "meerkat",
				},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Slices/Strings/Empty/True",
			args: equalitorArgs{
				a: []string{},
				b: []string{},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
		{
			description: "Slices/Strings/Sequence/True",
			args: equalitorArgs{
				a: []string{"hippo", "meerkat"},
				b: []string{"hippo", "meerkat"},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
		{
			description: "Slices/Strings/Sequence/False",
			args: equalitorArgs{
				a: []string{"hippo", "meerkat"},
				b: []string{"meerkat", "hippo"},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Slices/Strings/Sequence/LengthChange/False",
			args: equalitorArgs{
				a: []string{"hippo", "meerkat"},
				b: []string{"hippo", "meerkat", "otter"},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Slices/Structs/Exported/Sequence/True",
			args: equalitorArgs{
				a: []struct {
					Animal string
				}{
					{Animal: "hippo"},
					{Animal: "meerkat"},
				},
				b: []struct {
					Animal string
				}{
					{Animal: "hippo"},
					{Animal: "meerkat"},
				},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
		{
			description: "Slices/Structs/Exported/Sequence/False",
			args: equalitorArgs{
				a: []struct {
					Animal string
				}{
					{Animal: "hippo"},
					{Animal: "meerkat"},
				},
				b: []struct {
					Animal string
				}{
					{Animal: "meerkat"},
					{Animal: "hippo"},
				},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Slices/Structs/Exported/Sequence/LengthChange/False",
			args: equalitorArgs{
				a: []struct {
					Animal string
				}{
					{Animal: "hippo"},
					{Animal: "meerkat"},
				},
				b: []struct {
					Animal string
				}{
					{Animal: "hippo"},
					{Animal: "meerkat"},
					{Animal: "otter"},
				},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Slice/Structs/Strings/MismatchedTypes/False",
			args: equalitorArgs{
				a: []struct {
					Animal string
				}{
					{Animal: "hippo"},
					{Animal: "meerkat"},
				},
				b: []string{"hippo", "meerkat"},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Struct/int/MismatchedTypes/False",
			args: equalitorArgs{
				a: struct {
					Animal string
				}{
					Animal: "hippo",
				},
				b: 5,
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Struct/nil/MismatchedTypes/False",
			args: equalitorArgs{
				a: struct {
					Animal string
				}{
					Animal: "hippo",
				},
				b: nil,
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Map/Strings/True",
			args: equalitorArgs{
				a: map[string]int{
					"hippo":   64,
					"meerkat": 32,
				},
				b: map[string]int{
					"hippo":   64,
					"meerkat": 32,
				},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
		{
			description: "Map/Strings/Set/True",
			args: equalitorArgs{
				a: map[string]int{
					"hippo":   64,
					"meerkat": 32,
				},
				b: map[string]int{
					"meerkat": 32,
					"hippo":   64,
				},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
	}
)

func RunEqualitorSuite(equalitor Equalitor, suite []equalitorTest, t *testing.T) {
	for _, tt := range suite {
		t.Run(tt.description, func(t *testing.T) {
			// Check equality and ensure symetry
			require.Equal(t, tt.wants.equal, equalitor.Equal(tt.args.a, tt.args.b))
			require.Equal(t, tt.wants.equal, equalitor.Equal(tt.args.b, tt.args.a))
		})
	}
}

func TestNewHashEqualitor(t *testing.T) {
	// Run the standard test suite
	equalitor := NewHashEqualitor()
	RunEqualitorSuite(equalitor, standardSuite, t)

	// Run custom tests for the specific Equalitor
	type Animal struct {
		Name string
	}
	suite := []equalitorTest{
		{
			description: "Structs/Slices/NoSetTag/False",
			args: equalitorArgs{
				a: struct {
					Animals []Animal
				}{
					Animals: []Animal{
						{Name: "hippo"},
						{Name: "meerkat"},
					},
				},
				b: struct {
					Animals []Animal
				}{
					Animals: []Animal{
						{Name: "meerkat"},
						{Name: "hippo"},
					},
				},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Structs/Slices/SetTag/True",
			args: equalitorArgs{
				a: struct {
					Animals []Animal `hash:"set"`
				}{
					Animals: []Animal{
						{Name: "hippo"},
						{Name: "meerkat"},
					},
				},
				b: struct {
					Animals []Animal `hash:"set"`
				}{
					Animals: []Animal{
						{Name: "meerkat"},
						{Name: "hippo"},
					},
				},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
		{
			description: "Structs/Field/Changed/NoIgnoreTag/False",
			args: equalitorArgs{
				a: struct {
					Animal
					Age int
				}{
					Animal: Animal{
						Name: "hippo",
					},
					Age: 27,
				},
				b: struct {
					Animal
					Age int
				}{
					Animal: Animal{
						Name: "hippo",
					},
					Age: 28,
				},
			},
			wants: equalitorWants{
				equal: false,
			},
		},
		{
			description: "Structs/Field/Changed/IgnoreTag/True",
			args: equalitorArgs{
				a: struct {
					Animal
					Age int `hash:"ignore"`
				}{
					Animal: Animal{
						Name: "hippo",
					},
					Age: 27,
				},
				b: struct {
					Animal
					Age int `hash:"ignore"`
				}{
					Animal: Animal{
						Name: "hippo",
					},
					Age: 28,
				},
			},
			wants: equalitorWants{
				equal: true,
			},
		},
	}
	RunEqualitorSuite(equalitor, suite, t)
}
