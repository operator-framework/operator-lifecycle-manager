package operators

import (
	"encoding/json"

	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

const (
	// The yaml attribute that specifies the related images of the ClusterServiceVersion
	relatedImages = "relatedImages"
)

// CreateCSVDescription creates a CSVDescription from a given CSV
func CreateCSVDescription(csv *operatorsv1alpha1.ClusterServiceVersion, csvJSON string) CSVDescription {
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
		NativeAPIs:     csv.Spec.NativeAPIs,
		MinKubeVersion: csv.Spec.MinKubeVersion,
		RelatedImages:  GetImages(csvJSON),
		Keywords:       csv.Spec.Keywords,
		Maturity:       csv.Spec.Maturity,
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

	desc.Links = make([]AppLink, len(csv.Spec.Links))
	for i, link := range csv.Spec.Links {
		desc.Links[i] = AppLink{
			Name: link.Name,
			URL:  link.URL,
		}
	}

	desc.Maintainers = make([]Maintainer, len(csv.Spec.Maintainers))
	for i, maintainer := range csv.Spec.Maintainers {
		desc.Maintainers[i] = Maintainer{
			Name:  maintainer.Name,
			Email: maintainer.Email,
		}
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

// GetImages returns a list of images listed in CSV (spec and deployments)
func GetImages(csvJSON string) []string {
	var images []string

	csv := &opregistry.ClusterServiceVersion{}
	err := json.Unmarshal([]byte(csvJSON), &csv)
	if err != nil {
		return images
	}

	imageSet, err := csv.GetOperatorImages()
	if err != nil {
		return images
	}

	relatedImgSet, err := csv.GetRelatedImages()
	if err != nil {
		return images
	}

	for k := range relatedImgSet {
		imageSet[k] = struct{}{}
	}

	for k := range imageSet {
		images = append(images, k)
	}

	return images
}
