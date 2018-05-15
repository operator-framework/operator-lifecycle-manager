package client

import (
	"encoding/json"
	"fmt"
	"strings"

	appsv1beta2 "k8s.io/api/apps/v1beta2"
	"k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	v1beta1ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"github.com/coreos-inc/tectonic-operators/lib/manifest/diff"
)

func createPatch(original, modified runtime.Object) ([]byte, error) {
	originalData, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	modifiedData, err := json.Marshal(modified)
	if err != nil {
		return nil, err
	}
	return strategicpatch.CreateTwoWayMergePatch(originalData, modifiedData, original)
}

func createThreeWayMergePatchPreservingCommands(original, modified, current runtime.Object) ([]byte, error) {
	var datastruct runtime.Object
	switch {
	case original != nil:
		datastruct = original
	case modified != nil:
		datastruct = modified
	case current != nil:
		datastruct = current
	default:
		return nil, nil // A 3-way merge of `nil`s is `nil`.
	}
	patchMeta, err := strategicpatch.NewPatchMetaFromStruct(datastruct)
	if err != nil {
		return nil, err
	}

	// Create normalized clones of objects.
	original, err = cloneAndNormalizeObject(original)
	if err != nil {
		return nil, err
	}
	modified, err = cloneAndNormalizeObject(modified)
	if err != nil {
		return nil, err
	}
	current, err = cloneAndNormalizeObject(current)
	if err != nil {
		return nil, err
	}
	// Perform 3-way merge of annotations and labels.
	if err := mergeAnnotationsAndLabels(original, modified, current); err != nil {
		return nil, err
	}
	// Perform 3-way merge of container commands.
	if err := mergeContainerCommands(original, modified, current); err != nil {
		return nil, err
	}
	// Construct 3-way JSON merge patch.
	originalData, err := json.Marshal(original)
	if err != nil {
		return nil, err
	}
	modifiedData, err := json.Marshal(modified)
	if err != nil {
		return nil, err
	}
	currentData, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}
	return strategicpatch.CreateThreeWayMergePatch(originalData, modifiedData, currentData, patchMeta, false /* overwrite */)
}

func cloneAndNormalizeObject(obj runtime.Object) (runtime.Object, error) {
	if obj == nil {
		return obj, nil
	}

	// Clone the object since it will be modified.
	obj = obj.DeepCopyObject()
	switch obj := obj.(type) {
	case *appsv1beta2.DaemonSet:
		if obj != nil {
			// These are only extracted from current; should not be considered for diffs.
			obj.ObjectMeta.ResourceVersion = ""
			obj.ObjectMeta.CreationTimestamp = metav1.Time{}
			obj.Status = appsv1beta2.DaemonSetStatus{}
		}
	case *appsv1beta2.Deployment:
		if obj != nil {
			// These are only extracted from current; should not be considered for diffs.
			obj.ObjectMeta.ResourceVersion = ""
			obj.ObjectMeta.CreationTimestamp = metav1.Time{}
			obj.Status = appsv1beta2.DeploymentStatus{}
		}
	case *v1.Service:
		if obj != nil {
			// These are only extracted from current; should not be considered for diffs.
			obj.ObjectMeta.ResourceVersion = ""
			obj.ObjectMeta.CreationTimestamp = metav1.Time{}
			obj.Status = v1.ServiceStatus{}
			// ClusterIP for service is immutable, so cannot patch.
			obj.Spec.ClusterIP = ""
		}
	case *extensionsv1beta1.Ingress:
		if obj != nil {
			// These are only extracted from current; should not be considered for diffs.
			obj.ObjectMeta.ResourceVersion = ""
			obj.ObjectMeta.CreationTimestamp = metav1.Time{}
			obj.Status = extensionsv1beta1.IngressStatus{}
		}
	case *v1beta1ext.CustomResourceDefinition:
		if obj != nil {
			// These are only extracted from current; should not be considered for diffs.
			obj.ObjectMeta.ResourceVersion = ""
			obj.ObjectMeta.CreationTimestamp = metav1.Time{}
			obj.ObjectMeta.SelfLink = ""
			obj.ObjectMeta.UID = ""
			obj.Status = v1beta1ext.CustomResourceDefinitionStatus{}
		}
	default:
		return nil, fmt.Errorf("unhandled type: %T", obj)
	}
	return obj, nil
}

func cloneContainers(src []v1.Container) []v1.Container {
	dst := make([]v1.Container, len(src))
	copy(dst, src)
	return dst
}

func mergeContainerCommands(original, modified, current runtime.Object) error {
	// Extract containers from Deployments/DaemonSets.
	originalCs := extractContainers(original)
	modifiedCs := extractContainers(modified)
	currentCs := extractContainers(current)

	// Union all container names.
	names := make(map[string]interface{}, len(originalCs)+len(modifiedCs)+len(currentCs))
	for _, c := range originalCs {
		names[c.Name] = struct{}{}
	}
	for _, c := range modifiedCs {
		names[c.Name] = struct{}{}
	}
	for _, c := range currentCs {
		names[c.Name] = struct{}{}
	}

	// Perform 3-way merge on Command slices for each container by name.
	for name := range names {
		originalC := lookupContainer(originalCs, name)
		modifiedC := lookupContainer(modifiedCs, name)
		currentC := lookupContainer(currentCs, name)

		if err := mergeContainerCommand(originalC, modifiedC, currentC); err != nil {
			return err
		}
	}
	return nil
}

func lookupContainer(containers []v1.Container, name string) *v1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

// mergeContainerCommand performs a 3-way merge for the Command slices in a single container.
func mergeContainerCommand(original, modified, current *v1.Container) error {
	var originalCommand, modifiedCommand, currentCommand []string
	if original != nil {
		originalCommand = original.Command
	}
	if modified != nil {
		modifiedCommand = modified.Command
	}
	if current != nil {
		currentCommand = current.Command
	}
	// Perform the 3-way merge.
	merged, err := mergeCommands(originalCommand, modifiedCommand, currentCommand)
	if err != nil {
		return err
	}
	// If the flags are unchanged do nothing.
	if len(merged) == len(currentCommand) {
		commandChanged := false
		for i := range merged {
			if merged[i] != currentCommand[i] {
				commandChanged = true
				break
			}
		}
		if !commandChanged {
			if modified != nil {
				modified.Command = merged
			}
			return nil
		}
	}
	// To ensure that the merged commands are used by the JSON 3-way patch logic, we make sure that
	// only `modified` (or `current` if `modified` is nil) has the slice of commands, and all others are nil.
	if original != nil {
		original.Command = nil
	}
	if current != nil {
		current.Command = nil
	}
	if modified != nil {
		modified.Command = merged
	} else if current != nil {
		current.Command = merged
	}
	return nil
}

func extractContainers(obj runtime.Object) []v1.Container {
	switch obj := obj.(type) {
	case *appsv1beta2.DaemonSet:
		if obj == nil {
			return nil
		}
		return obj.Spec.Template.Spec.Containers
	case *appsv1beta2.Deployment:
		if obj == nil {
			return nil
		}
		return obj.Spec.Template.Spec.Containers
	default:
		return nil
	}
}

// mergeCommands performs a 3-way diff preserving templated values from currentCommand.
func mergeCommands(originalCommand, modifiedCommand, currentCommand []string) ([]string, error) {
	// Normalize commands & extract template vars.
	vars := make(map[string]string)
	original := make([]diff.Eq, len(originalCommand))
	for i, c := range originalCommand {
		f := parseCommand(c)
		original[i] = f
		if f.templated {
			vars[f.key] = "${undefined}"
		}
	}
	modified := make([]diff.Eq, len(modifiedCommand))
	for i, c := range modifiedCommand {
		f := parseCommand(c)
		modified[i] = f
		if f.templated {
			vars[f.key] = "${undefined}"
		}
	}
	current := make([]diff.Eq, len(currentCommand))
	for i, c := range currentCommand {
		f := parseCommand(c)
		current[i] = f
		if _, ok := vars[f.key]; ok {
			vars[f.key] = f.val
		}
	}
	// Perform 3-way diff merge.
	merged, err := diff.Diff3Merge(original, modified, current)
	if err != nil {
		return nil, err
	}
	// Construct output, re-insterting template values.
	out := make([]string, len(merged))
	for i := range merged {
		f := merged[i].(command)
		if f.templated {
			val, ok := vars[f.key]
			if !ok || strings.HasPrefix(val, "${") {
				return nil, fmt.Errorf("no value found for templated command %q", f.key)
			}
			f.val = val
		}
		out[i] = f.String()
	}
	return out, nil
}

type command struct {
	key       string
	val       string
	keyval    bool
	templated bool
}

func parseCommand(strCommand string) command {
	parts := strings.SplitN(strCommand, "=", 2)
	key := parts[0]
	val := ""
	keyval := len(parts) > 1
	templated := false
	if keyval {
		val = parts[1]
		templated = strings.Contains(val, "${")
	}
	return command{
		key:       key,
		val:       val,
		keyval:    keyval,
		templated: templated,
	}
}

func (f command) Eq(e diff.Eq) bool {
	o := e.(command)
	if f.keyval != o.keyval {
		return false
	}
	if f.templated != o.templated {
		return f.key == o.key
	}
	return f.key == o.key && f.val == o.val
}

func (f command) String() string {
	if f.keyval {
		return fmt.Sprintf("%s=%s", f.key, f.val)
	}
	return f.key
}

// mergeAnnotationsAndLabels performs a 3-way merge of all annotations and labels using custom
// 3-way merge logic defined in mergeMaps() below.
func mergeAnnotationsAndLabels(original, modified, current runtime.Object) error {
	if original == nil || modified == nil || current == nil {
		return nil
	}

	accessor := meta.NewAccessor()
	if err := mergeMaps(original, modified, current, accessor.Annotations, accessor.SetAnnotations); err != nil {
		return err
	}
	if err := mergeMaps(original, modified, current, accessor.Labels, accessor.SetLabels); err != nil {
		return err
	}

	switch current := current.(type) {
	case *appsv1beta2.DaemonSet:
		getter := func(obj runtime.Object) (map[string]string, error) {
			return obj.(*appsv1beta2.DaemonSet).Spec.Template.Annotations, nil
		}
		setter := func(obj runtime.Object, val map[string]string) error {
			obj.(*appsv1beta2.DaemonSet).Spec.Template.Annotations = val
			return nil
		}
		if err := mergeMaps(original, modified, current, getter, setter); err != nil {
			return err
		}
		getter = func(obj runtime.Object) (map[string]string, error) {
			return obj.(*appsv1beta2.DaemonSet).Spec.Template.Labels, nil
		}
		setter = func(obj runtime.Object, val map[string]string) error {
			obj.(*appsv1beta2.DaemonSet).Spec.Template.Labels = val
			return nil
		}
		if err := mergeMaps(original, modified, current, getter, setter); err != nil {
			return err
		}
	case *appsv1beta2.Deployment:
		getter := func(obj runtime.Object) (map[string]string, error) {
			return obj.(*appsv1beta2.Deployment).Spec.Template.Annotations, nil
		}
		setter := func(obj runtime.Object, val map[string]string) error {
			obj.(*appsv1beta2.Deployment).Spec.Template.Annotations = val
			return nil
		}
		if err := mergeMaps(original, modified, current, getter, setter); err != nil {
			return err
		}
		getter = func(obj runtime.Object) (map[string]string, error) {
			return obj.(*appsv1beta2.Deployment).Spec.Template.Labels, nil
		}
		setter = func(obj runtime.Object, val map[string]string) error {
			obj.(*appsv1beta2.Deployment).Spec.Template.Labels = val
			return nil
		}
		if err := mergeMaps(original, modified, current, getter, setter); err != nil {
			return err
		}
	}
	return nil
}

// mergeMaps creates a patch using createThreeWayMapPatch and if the patch is non-empty applies
// the patch to the input. The getter and setter are used to access the map inside the given
// objects.
func mergeMaps(original, modified, current runtime.Object, getter func(runtime.Object) (map[string]string, error), setter func(runtime.Object, map[string]string) error) error {
	originalMap, err := getter(original)
	if err != nil {
		return err
	}
	modifiedMap, err := getter(modified)
	if err != nil {
		return err
	}
	currentMap, err := getter(current)
	if err != nil {
		return err
	}

	patch, err := createThreeWayMapPatch(originalMap, modifiedMap, currentMap)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return nil // nothing to apply.
	}
	modifiedMap = applyMapPatch(originalMap, currentMap, patch)

	if err := setter(original, originalMap); err != nil {
		return err
	}
	if err := setter(modified, modifiedMap); err != nil {
		return err
	}
	return setter(current, currentMap)
}

// applyMapPatch creates a copy of current and applies the three-way map patch to it.
func applyMapPatch(original, current map[string]string, patch map[string]interface{}) map[string]string {
	merged := make(map[string]string, len(current))
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = v.(string)
			if _, ok := current[k]; !ok {
				// If we are re-adding something that may have already been in original then ensure it is
				// removed from `original` to avoid a conflict in upstream patch code.
				delete(original, k)
			}
		}
	}
	return merged
}

// createThreeWayMapPatch constructs a 3-way patch between original, modified, and current. The
// patch contains only keys that are added, keys that are removed (with their values set to nil) or
// keys whose values are modified. Returns an error if there is a conflict for any key.
//
// The behavior is defined as follows:
//
// - If an item is present in modified, ensure it exists in current.
// - If an item is present in original and removed in modified, remove it from current.
// - If an item is present only in current, leave it as-is.
//
// This effectively "enforces" that all items present in modified are present in current, and all
// items deleted from original => modified are deleted in current.
//
// The following will cause a conflict:
//
// (1) An item was deleted from original => modified but modified from original => current.
// (2) An item was modified differently from original => modified and original => current.
func createThreeWayMapPatch(original, modified, current map[string]string) (map[string]interface{}, error) {
	// Create union of keys.
	keys := make(map[string]struct{})
	for k := range original {
		keys[k] = struct{}{}
	}
	for k := range modified {
		keys[k] = struct{}{}
	}
	for k := range current {
		keys[k] = struct{}{}
	}

	// Create patch according to rules.
	patch := make(map[string]interface{})
	for k := range keys {
		oVal, oOk := original[k]
		mVal, mOk := modified[k]
		cVal, cOk := current[k]

		switch {
		case oOk && mOk && cOk:
			// present in all three.
			if mVal != cVal {
				if oVal != cVal {
					// conflict type 2: changed to different values in modified and current.
					return nil, fmt.Errorf("conflict at key %v: original = %v, modified = %v, current = %v", k, oVal, mVal, cVal)
				}
				patch[k] = mVal
			}
		case !oOk && mOk && cOk:
			// added in modified and current.
			if mVal != cVal {
				// conflict type 2: added different values in modified and current.
				return nil, fmt.Errorf("conflict at key %v: original = <absent>, modified = %v, current = %v", k, mVal, cVal)
			}
		case oOk && !mOk && cOk:
			// deleted in modified.
			if oVal != cVal {
				// conflict type 1: changed from original to current, removed in modified.
				return nil, fmt.Errorf("conflict at key %v, original = %v, modified = <absent>, current = %v", k, oVal, cVal)
			}
			patch[k] = nil
		case oOk && mOk && !cOk:
			// deleted in current.
			patch[k] = mVal
		case !oOk && !mOk && cOk:
			// only exists in current.
		case !oOk && mOk && !cOk:
			// added in modified.
			patch[k] = mVal
		case oOk && !mOk && !cOk:
			// deleted in both modified and current.
		case !oOk && !mOk && !cOk:
			// unreachable.
		}
	}
	return patch, nil
}
