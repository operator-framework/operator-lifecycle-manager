package operators

import operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"

// CreateCSVDescription creates a CSVDescription from a given CSV
func CreateCSVDescription(csv *operatorsv1alpha1.ClusterServiceVersion) CSVDescription {
	desc := CSVDescription{
		DisplayName: csv.Spec.DisplayName,
		Version:     csv.Spec.Version,
		Provider: AppLink{
			Name: csv.Spec.Provider.Name,
			URL:  csv.Spec.Provider.URL,
		},
		Annotations:     csv.GetAnnotations(),
		LongDescription: csv.Spec.Description,
		InstallModes:    csv.Spec.InstallModes,
		CustomResourceDefinitions: operatorsv1alpha1.CustomResourceDefinitions{
			Owned:    descriptionsForCRDs(csv.Spec.CustomResourceDefinitions.Owned),
			Required: descriptionsForCRDs(csv.Spec.CustomResourceDefinitions.Required),
		},
		APIServiceDefinitions: operatorsv1alpha1.APIServiceDefinitions{
			Owned:    descriptionsForAPIServices(csv.Spec.APIServiceDefinitions.Owned),
			Required: descriptionsForAPIServices(csv.Spec.APIServiceDefinitions.Required),
		},
	}

	icons := make([]Icon, len(csv.Spec.Icon))
	for i, icon := range csv.Spec.Icon {
		icons[i] = Icon{
			Base64Data: icon.Data,
			Mediatype:  icon.MediaType,
		}
	}

	if len(icons) > 0 {
		desc.Icon = icons
	}

	return desc
}

// descriptionsForCRDs filters certain fields from provided API descriptions to reduce response size.
func descriptionsForCRDs(crds []operatorsv1alpha1.CRDDescription) []operatorsv1alpha1.CRDDescription {
	descriptions := []operatorsv1alpha1.CRDDescription{}
	for _, crd := range crds {
		descriptions = append(descriptions, operatorsv1alpha1.CRDDescription{
			Name:        crd.Name,
			Version:     crd.Version,
			Kind:        crd.Kind,
			DisplayName: crd.DisplayName,
			Description: crd.Description,
		})
	}
	return descriptions
}

// descriptionsForAPIServices filters certain fields from provided API descriptions to reduce response size.
func descriptionsForAPIServices(apis []operatorsv1alpha1.APIServiceDescription) []operatorsv1alpha1.APIServiceDescription {
	descriptions := []operatorsv1alpha1.APIServiceDescription{}
	for _, api := range apis {
		descriptions = append(descriptions, operatorsv1alpha1.APIServiceDescription{
			Name:        api.Name,
			Group:       api.Group,
			Version:     api.Version,
			Kind:        api.Kind,
			DisplayName: api.DisplayName,
			Description: api.Description,
		})
	}
	return descriptions
}
