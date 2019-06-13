package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
)

func addConversionFuncs(scheme *runtime.Scheme) error {
	// Register non-generated conversion functions
	err := scheme.AddConversionFuncs(
	// Convert_v1alpha1_FlunderSpec_To_wardle_FlunderSpec,
	// Convert_wardle_FlunderSpec_To_v1alpha1_FlunderSpec,
	)
	if err != nil {
		return err
	}

	return nil
}
