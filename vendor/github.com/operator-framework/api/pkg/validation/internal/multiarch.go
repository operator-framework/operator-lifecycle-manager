package internal

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

// MultipleArchitecturesValidator validates the bundle against criteria to support Multiple Architectures. For further
// information check: https://olm.operatorframework.io/docs/advanced-tasks/ship-operator-supporting-multiarch/
//
// This validator will inspect the images with the chosen container-tool. One of: [docker, podman, none] (By default docker)
// and then: (It is only used to $container-tool manifest inspect)
//
// - raise a error(s) when is possible to confirm that images do not provide the support defined via to the labels in the CSV
//
// - raise a warning when it is possible to check that the Operator manager image(s) supports architecture(s) not defined via labels. Therefore, it shows like the labels are missing.
//
// - raise warnings when it is possible to verify that the images defined in the CSV do not provide the same architecture(s) supported by the Operator manager image(s) or defined via the labels
//
// ### What is checked?
//
// On this check, we aggregate the archetype(s) and OS(s) provided via the labels and those which are found by checking the images so that, we can check:
//
// - If your CSV is missing labels
//
// - If your Operator bundle specifies images which do not support all archetypes found for your Operator image(s) (probably supported by your project)
//
// Note: To better guess the case scenarios where authors might have missed the labels, the following check will verify all architectures supported by the Operator image(s). However, by looking at the CSV we are not able to ensure what is the Operator image because this info is not provided. Therefore, we know by SDK the Operator image container will be called manager.
//
// ### How the Operator image(s) are identified?
//
// The container named as manager under the CSV Deployment InstallStrategy (`Spec.InstallStrategy.StrategySpec.DeploymentSpecs`)
// And if the above not found, all images under the InstallStrategy excluding the container named as `kube-rbac-proxy` since it is also scaffolded by default via SDK
var MultipleArchitecturesValidator interfaces.Validator = interfaces.ValidatorFunc(multipleArchitecturesValidate)

// ContainerToolsKey defines the key which can be used by its consumers
// to inform where to find the container tool that should be used to inspect the image
const ContainerToolsKey = "container-tools"

// operatorFrameworkArchLabel defines the label used to store the supported Arch on CSV
const operatorFrameworkArchLabel = "operatorframework.io/arch."

// operatorFrameworkOSLabel stores the labels for the supported OS from the CSV
const operatorFrameworkOSLabel = "operatorframework.io/os."

// default_container_scaffold_by_sdk defines the name of a default scaffold done by SDK
// it is useful for we are able to find the operator manager image more assertively
const default_container_scaffold_by_sdk = "kube-rbac-proxy"

// multiArchValidator store the data to perform the tests
type multiArchValidator struct {
	// infraCSVArchLabels store the arch labels (i.e amd64, ppc64le) from
	// operatorframework.io/arch.<GOARCH>: supported
	infraCSVArchLabels []string
	// InfraCVSOSLabels store the OS labels from
	// operatorframework.io/os.<GOARCH>: supported
	infraCSVOSLabels []string
	// allOtherImages stores the allOtherImages defined in the bundle with the platform.arch supported
	allOtherImages map[string][]platform
	// managerImages stores the images that we could consider as from the manager
	managerImages map[string][]platform
	// managerImagesString stores the images only
	managerImagesString []string
	// managerArchs contains a map of the arch types found
	managerArchs map[string]string
	// managerOs contains a map of the OSes found
	managerOs map[string]string
	// Store the bundle load
	bundle *manifests.Bundle
	// containerTool defines the container tool which will be used to inspect the allOtherImages
	containerTool string
	// warns stores the errors faced by the validator to return the warnings
	warns []error
	// warns stores the errors faced by the validator to return the warnings
	errors []error
}

// manifestInspect store the data obtained by running container-tool manifest inspect <IMAGE>
type manifestInspect struct {
	ManifestData []manifestData `json:"manifests"`
}

// manifestData store the platforms
type manifestData struct {
	Platform platform `json:"platform"`
}

// platform store the Architecture and OS supported by the image
type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

func multipleArchitecturesValidate(objs ...interface{}) (results []errors.ManifestResult) {
	// Obtain the k8s version if informed via the objects an optional
	var containerTool = ""
	for _, obj := range objs {
		switch obj.(type) {
		case map[string]string:
			// Check the key values informed
			containerTool = obj.(map[string]string)[ContainerToolsKey]
			if len(containerTool) > 0 {
				// Make lower for we compare and use it
				containerTool = strings.ToLower(containerTool)
				break
			}
		}
	}

	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateMultiArchWith(v, containerTool))
		}
	}
	return results
}

func validateMultiArchWith(bundle *manifests.Bundle, containerTool string) errors.ManifestResult {
	result := errors.ManifestResult{}
	if bundle == nil {
		result.Add(errors.ErrInvalidBundle("bundle is nil", nil))
		return result
	}

	result.Name = bundle.Name

	if bundle.CSV == nil {
		result.Add(errors.ErrInvalidBundle("bundle csv is nil", bundle.Name))
		return result
	}

	// Validate inputs. If a container-tool key be informed
	// with an invalid/unsupported value then make no sense do the check
	containerTool, err := validateContainerTool(containerTool)
	if err != nil {
		result.Add(errors.ErrFailedValidation(err.Error(), bundle.CSV.GetName()))
		return result
	}

	// Performs the checks

	multiArchValidator := multiArchValidator{bundle: bundle, containerTool: containerTool}
	multiArchValidator.validate()

	for _, err := range multiArchValidator.warns {
		// add the warn to the result
		result.Add(errors.WarnFailedValidation(err.Error(), bundle.CSV.GetName()))
	}

	for _, err := range multiArchValidator.errors {
		// add the warn to the result
		result.Add(errors.ErrFailedValidation(err.Error(), bundle.CSV.GetName()))
	}

	return result
}

// validateContainerTool verifies if the container tool informed is valid
func validateContainerTool(containerTool string) (string, error) {
	if len(containerTool) == 0 || containerTool == "none" {
		containerTool = "docker"
	} else if containerTool != "docker" && containerTool != "podman" {
		return containerTool, fmt.Errorf("invalid value (%s) for (%s). One of: [docker, podman, none] "+
			"(If not set, the default value is docker)", ContainerToolsKey, containerTool)
	}
	return containerTool, nil
}

// validate performs all required checks to validate the bundle against the Multiple Architecture
// configuration to guess the missing labels and/or highlight what are the missing Architectures
// for the allOtherImages (for what is configured to be supported AND for what we guess that is supported
// and just is missing a label).
func (data *multiArchValidator) validate() {
	data.loadInfraLabelsFromCSV()
	data.loadImagesFromCSV()
	data.managerImages = data.inspectImages(data.managerImages)
	data.allOtherImages = data.inspectImages(data.allOtherImages)
	data.loadAllPossibleArchSupported()
	data.loadAllPossibleSoSupported()
	data.doChecks()
}

// loadInfraLabelsFromCSV will gather the respective labels from the CSV
func (data *multiArchValidator) loadInfraLabelsFromCSV() {
	data.managerArchs = make(map[string]string)
	data.managerOs = make(map[string]string)

	for k, v := range data.bundle.CSV.ObjectMeta.Labels {
		if strings.Contains(k, operatorFrameworkArchLabel) && v == "supported" {
			data.infraCSVArchLabels = append(data.infraCSVArchLabels, k)
		}
	}
	for k, v := range data.bundle.CSV.ObjectMeta.Labels {
		if strings.Contains(k, operatorFrameworkOSLabel) && v == "supported" {
			data.infraCSVOSLabels = append(data.infraCSVOSLabels, k)
		}
	}
}

// loadImagesFromCSV will add all allOtherImages found in the CSV
// it will be looking for all containers allOtherImages and what is defined
// via the spec.relatedImages (required for disconnect support)
func (data *multiArchValidator) loadImagesFromCSV() {
	// We need to try looking for the manager image so that we can
	// be more assertive in the guess to warning the Operator
	// authors that when forgotten to use add the labels
	// because we found images that provides more support
	data.managerImages = make(map[string][]platform)
	for _, v := range data.bundle.CSV.Spec.InstallStrategy.StrategySpec.DeploymentSpecs {
		foundManager := false
		// For the default scaffold we have a container called manager
		for _, c := range v.Spec.Template.Spec.Containers {
			if c.Name == "manager" && len(data.managerImages[c.Image]) == 0 {
				data.managerImages[c.Image] = append(data.managerImages[c.Image], platform{})
				data.managerImagesString = append(data.managerImagesString, c.Image)
				foundManager = true
				break
			}
		}
		// If we do not find a container called manager then we
		// will add all from the Deployment Specs which is not the
		// kube-rbac-proxy image scaffold by default
		if !foundManager {
			for _, c := range v.Spec.Template.Spec.Containers {
				if c.Name != default_container_scaffold_by_sdk && len(data.managerImages[c.Image]) == 0 {
					data.managerImages[c.Image] = append(data.managerImages[c.Image], platform{})
					data.managerImagesString = append(data.managerImagesString, c.Image)
				}
			}
		}
	}

	data.allOtherImages = make(map[string][]platform)
	if data.bundle.CSV.Spec.InstallStrategy.StrategySpec.DeploymentSpecs != nil {
		for _, v := range data.bundle.CSV.Spec.InstallStrategy.StrategySpec.DeploymentSpecs {
			for _, c := range v.Spec.Template.Spec.Containers {
				// If not be from manager the add
				if len(data.managerImages[c.Image]) == 0 {
					data.allOtherImages[c.Image] = append(data.allOtherImages[c.Image], platform{})
				}
			}
		}
	}

	for _, v := range data.bundle.CSV.Spec.RelatedImages {
		data.allOtherImages[v.Image] = append(data.allOtherImages[v.Image], platform{})
	}
}

// runManifestInspect executes the command for we are able to check what
// are the Architecture(s) and SO(s) supported per each image found
func runManifestInspect(image, tool string) (manifestInspect, error) {
	cmd := exec.Command(tool, "pull", image)
	_, err := runCommand(cmd)
	if err != nil {
		return manifestInspect{}, err
	}

	cmd = exec.Command(tool, "manifest", "inspect", image)
	output, err := runCommand(cmd)
	if err != nil {
		return manifestInspect{}, err
	}

	var inspect manifestInspect
	if err := json.Unmarshal(output, &inspect); err != nil {
		return manifestInspect{}, err
	}
	return inspect, nil
}

// run executes the provided command within this context
func runCommand(cmd *exec.Cmd) ([]byte, error) {
	command := strings.Join(cmd.Args, " ")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed with error: (%v) %s", command, err, string(output))
	}
	return output, nil
}

// inspectAllOtherImages will perform the required steps to inspect all allOtherImages found
func (data *multiArchValidator) inspectImages(images map[string][]platform) map[string][]platform {
	for k := range images {
		manifest, err := runManifestInspect(k, data.containerTool)
		if err != nil {
			// try once more
			manifest, err = runManifestInspect(k, data.containerTool)
			if err != nil {
				data.warns = append(data.warns, fmt.Errorf("unable to inspect the image (%s) : %s", k, err))

				// We set the Arch and SO as error for we are able to deal witth these cases further
				// Se that make no sense we raise a warning to notify the user that the image
				// does not provide some kind of support only because we were unable to inspect it.
				// Be aware that the validator raise warnings for all cases scenarios to let
				// the author knows that those were not checked at all and why.
				images[k][0] = platform{"error", "error"}
				continue
			}
		}

		if manifest.ManifestData != nil {
			for _, manifest := range manifest.ManifestData {
				images[k] = append(images[k], manifest.Platform)
			}
		}
	}
	return images
}

// doChecks centralize all checks which are done with this validator
func (data *multiArchValidator) doChecks() {
	// the following check raise a error(s) when is possible to confirm that images does not provide the
	// support defined via to the labels on the CSV
	data.checkSupportDefined()
	// Note that we can only check if the CSV is missing or not label after check all possible arch/so supported
	// on the check above. The following check raise a warning when it is possible to check that the Operator
	// manager image(s) supports architecture(s) not defined via labels. Therefore, it shows like the labels are missing
	data.checkMissingLabelsForArchs()
	data.checkMissingLabelsForSO()
	// the following check will raise warnings when is possible to verify that the images defined in the CSV
	// does not provide the same architecture(s) supported by the Operator manager or defined via the labels
	data.checkMissingSupportForOtherImages()
}

// checkMissingSupportForOtherImages checks if any image is missing some arch or so found
// (probably the image should support the arch or SO )
func (data *multiArchValidator) checkMissingSupportForOtherImages() {
	for image, plaformFromImage := range data.allOtherImages {
		listArchNotFound := []string{}
		for archFromList := range data.managerArchs {
			found := false
			for _, imageData := range plaformFromImage {
				// Ignore the case when the Plataform.Architecture == "error" since that means
				// that was not possible to inspect the image
				if imageData.Architecture == "error" {
					found = true
					break
				}

				if imageData.Architecture == archFromList {
					found = true
					break
				}
			}
			if !found && archFromList != "error" {
				listArchNotFound = append(listArchNotFound, archFromList)
			}
		}
		if len(listArchNotFound) > 0 {
			sort.Strings(listArchNotFound)
			data.warns = append(data.warns,
				fmt.Errorf("check if the image %s should not support %q. "+
					"Note that this CSV has labels for this Arch(s) "+
					"Your manager image %q are providing this support OR the CSV is configured via labels "+
					"to support it. Then, please verify if this image should not support it",
					image,
					listArchNotFound,
					data.managerImagesString))
		}

		listAllSoNotFound := []string{}
		for archOSList := range data.managerOs {
			found := false
			for _, imageData := range plaformFromImage {
				// Ignore the case when the Plataform.Architecture == "error" since that means
				// that was not possible to inspect the image
				if imageData.OS == "error" {
					found = true
					break
				}

				if imageData.OS == archOSList {
					found = true
					break
				}
			}
			if !found && archOSList != "error" {
				listAllSoNotFound = append(listAllSoNotFound, archOSList)
			}
		}
		if len(listAllSoNotFound) > 0 {
			sort.Strings(listAllSoNotFound)
			data.warns = append(data.warns,
				fmt.Errorf("check if the image %s should not support %q. "+
					"Note that this CSV has labels for this SO(s) "+
					"Your manager image %q are providing this support OR the CSV is configured via labels "+
					"to support it. Then, please verify if this image should not support it",
					image,
					listAllSoNotFound,
					data.managerImagesString))
		}
	}
}

// verify if 1 or more allOtherImages has support for a SO not defined via the labels
// (probably the label for this SO is missing )
func (data *multiArchValidator) checkMissingLabelsForSO() {
	notFoundSoLabel := []string{}
	for supported := range data.managerOs {
		found := false
		for _, infra := range data.infraCSVOSLabels {
			if strings.Contains(infra, supported) {
				found = true
				break
			}
		}
		// If the value is linux and no labels were added to the CSV then it is fine
		if !found && supported != "error" {
			// if the only arch supported is linux then,  we should not ask for the label
			if !(supported == "linux" && len(data.managerOs) == 1 && len(data.managerOs["linux"]) > 0) {
				notFoundSoLabel = append(notFoundSoLabel, supported)
			}

		}
	}

	if len(notFoundSoLabel) > 0 {
		// We need to sort, otherwise it is possible verify in the tests that we have
		// this message as result
		sort.Strings(notFoundSoLabel)
		data.warns = append(data.warns,
			fmt.Errorf("check if the CSV is missing the label (%s<value>) for the SO(s): %q. "+
				"Be aware that your Operator manager image %q provides this support. "+
				"Thus, it is very likely that you want to provide it and if you support more than linux SO you MUST,"+
				"use the required labels for all which are supported."+
				"Otherwise, your solution cannot be listed on the cluster for these architectures",
				operatorFrameworkOSLabel,
				notFoundSoLabel,
				data.managerImagesString))
	}
}

// checkMissingLabelsForArchs verify if 1 or ore allOtherImages has support for a Arch not defined via the labels
// (probably the label for this Arch is missing )
func (data *multiArchValidator) checkMissingLabelsForArchs() {
	notFoundArchLabel := []string{}
	for supported := range data.managerArchs {
		found := false
		for _, infra := range data.infraCSVArchLabels {
			if strings.Contains(infra, supported) {
				found = true
				break
			}
		}
		// If the value is amd64 and no labels were added to the CSV then it is fine
		if !found && supported != "error" {
			// if the only arch supported is amd64 then we should not ask for the label
			if !(supported == "amd64" && len(data.managerArchs) == 1 && len(data.managerArchs["amd64"]) > 0) {
				notFoundArchLabel = append(notFoundArchLabel, supported)
			}
		}
	}

	if len(notFoundArchLabel) > 0 {
		// We need to sort, otherwise it is possible verify in the tests that we have
		// this message as result
		sort.Strings(notFoundArchLabel)

		data.warns = append(data.warns,
			fmt.Errorf("check if the CSV is missing the label (%s<value>) for the Arch(s): %q. "+
				"Be aware that your Operator manager image %q provides this support. "+
				"Thus, it is very likely that you want to provide it and if you support more than amd64 architectures, you MUST,"+
				"use the required labels for all which are supported."+
				"Otherwise, your solution cannot be listed on the cluster for these architectures",
				operatorFrameworkArchLabel,
				notFoundArchLabel,
				data.managerImagesString))
	}
}

func (data *multiArchValidator) loadAllPossibleArchSupported() {
	// Add the values provided via label
	for _, v := range data.infraCSVArchLabels {
		label := extractValueFromArchLabel(v)
		data.managerArchs[label] = label
	}

	// If a CSV does not include an arch label, it is treated as if it has the following AMD64 support label by default
	if len(data.infraCSVArchLabels) == 0 {
		data.managerArchs["amd64"] = "amd64"
	}

	// Get all ARCH from the provided allOtherImages
	for _, imageData := range data.managerImages {
		for _, plataform := range imageData {
			if len(plataform.Architecture) > 0 {
				data.managerArchs[plataform.Architecture] = plataform.Architecture
			}
		}
	}
}

// loadAllPossibleSoSupported will verify all SO that this bundle can support
// for then, we aare able to check if it is missing labels.
// Note:
// - we check what are the SO of all allOtherImages informed
// - we ensure that the linux SO will be added when none so labels were informed
// - we check all labels to know what are the SO(s) to obtain the list of them which the bundle is defining
func (data *multiArchValidator) loadAllPossibleSoSupported() {
	// Add the values provided via label
	for _, v := range data.infraCSVOSLabels {
		label := extractValueFromSoLabel(v)
		data.managerOs[label] = label
	}

	// If a ClusterServiceVersion does not include an os label, a target OS is assumed to be linux
	if len(data.infraCSVOSLabels) == 0 {
		data.managerOs["linux"] = "linux"
	}

	// Get all SO from the provided allOtherImages
	for _, imageData := range data.managerImages {
		for _, plataform := range imageData {
			if len(plataform.OS) > 0 {
				data.managerOs[plataform.OS] = plataform.OS
			}
		}
	}
}

// checkSupportDefined checks if all allOtherImages supports the ARCHs and SOs defined
func (data *multiArchValidator) checkSupportDefined() {
	configuredS0 := []string{}
	if len(data.infraCSVOSLabels) == 0 {
		configuredS0 = []string{"linux"}
	}

	for _, label := range data.infraCSVOSLabels {
		configuredS0 = append(configuredS0, extractValueFromSoLabel(label))
	}

	configuredArch := []string{}
	if len(data.infraCSVArchLabels) == 0 {
		configuredArch = []string{"amd64"}
	}

	for _, label := range data.infraCSVArchLabels {
		configuredArch = append(configuredArch, extractValueFromArchLabel(label))
	}

	allSupportedConfiguration := []string{}
	for _, so := range configuredS0 {
		for _, arch := range configuredArch {
			allSupportedConfiguration = append(allSupportedConfiguration, fmt.Sprintf("%s.%s", so, arch))
		}
	}

	notFoundImgPlat := map[string][]string{}
	for _, config := range allSupportedConfiguration {
		for image, allPlataformFromImage := range data.managerImages {
			found := false
			for _, imgPlat := range allPlataformFromImage {
				// Ignore the errors since they mean that was not possible to inspect
				// the image
				if imgPlat.OS == "error" {
					found = true
					break
				}

				if config == fmt.Sprintf("%s.%s", imgPlat.OS, imgPlat.Architecture) {
					found = true
					break
				}
			}

			if !found {
				notFoundImgPlat[config] = append(notFoundImgPlat[config], image)
			}
		}
		for image, allPlataformFromImage := range data.allOtherImages {
			found := false
			for _, imgPlat := range allPlataformFromImage {
				// Ignore the errors since they mean that was not possible to inspect
				// the image
				if imgPlat.OS == "error" {
					found = true
					break
				}

				if config == fmt.Sprintf("%s.%s", imgPlat.OS, imgPlat.Architecture) {
					found = true
					break
				}
			}

			if !found {
				notFoundImgPlat[config] = append(notFoundImgPlat[config], image)
			}
		}
	}

	if len(notFoundImgPlat) > 0 {
		for platform, images := range notFoundImgPlat {
			// If we not sort the allOtherImages we cannot check its result in the tests
			sort.Strings(images)
			data.errors = append(data.errors,
				fmt.Errorf("not all images specified are providing the support described via the CSV labels. "+
					"Note that (SO.architecture): (%s) was not found for the image(s) %s",
					platform, images))
		}
	}
}

// extractValueFromSoLabel returns only the value of the SO label (i.e. linux)
func extractValueFromSoLabel(v string) string {
	label := strings.ReplaceAll(v, operatorFrameworkOSLabel, "")
	return label
}

// extractValueFromArchLabel returns only the value of the ARCH label (i.e. amd64)
func extractValueFromArchLabel(v string) string {
	label := strings.ReplaceAll(v, operatorFrameworkArchLabel, "")
	return label
}
