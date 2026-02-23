package v1

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
		Annotations:               csv.GetAnnotations(),
		LongDescription:           csv.Spec.Description,
		InstallModes:              csv.Spec.InstallModes,
		CustomResourceDefinitions: csv.Spec.CustomResourceDefinitions,
		APIServiceDefinitions:     csv.Spec.APIServiceDefinitions,
		NativeAPIs:                csv.Spec.NativeAPIs,
		MinKubeVersion:            csv.Spec.MinKubeVersion,
		RelatedImages:             GetImages(csvJSON),
		Keywords:                  csv.Spec.Keywords,
		Maturity:                  csv.Spec.Maturity,
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
