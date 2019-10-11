package operator

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/reference"

	operatorsv2alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v2alpha1"
)

const (
	// ComponentLabelKeyPrefix is the key prefix used for labels marking operator component resources.
	ComponentLabelKeyPrefix = "operators.coreos.com/"

	newOperatorError       = "Cannot create new Operator: %s"
	componentLabelKeyError = "Cannot generate component label key: %s"
)

var componentScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(AddToScheme(componentScheme))
}

// OperatorNames returns a list of operator names extracted from the given labels.
func OperatorNames(labels map[string]string) (names []types.NamespacedName) {
	for key := range labels {
		if !strings.HasPrefix(key, ComponentLabelKeyPrefix) {
			continue
		}

		names = append(names, types.NamespacedName{
			Name: strings.TrimPrefix(key, ComponentLabelKeyPrefix),
		})
	}

	return
}

// Operator decorates a v2alpha1 Operator and provides convenience methods for managing it.
type Operator struct {
	*operatorsv2alpha1.Operator
}

// NewOperator returns a new Operator instance.
func NewOperator(operator *operatorsv2alpha1.Operator) (*Operator, error) {
	if operator == nil {
		return nil, fmt.Errorf(newOperatorError, "nil Operator argument")
	}

	o := &Operator{
		Operator: operator.DeepCopy(),
	}

	return o, nil
}

// ComponentLabelKey returns the operator's completed component label key
func (o *Operator) ComponentLabelKey() (string, error) {
	if o.GetName() == "" {
		return "", fmt.Errorf(componentLabelKeyError, "empty name field")
	}

	return ComponentLabelKeyPrefix + o.GetName(), nil
}

// ComponentLabelSelector returns a LabelSelector that matches this operator's component label.
func (o *Operator) ComponentLabelSelector() (*metav1.LabelSelector, error) {
	key, err := o.ComponentLabelKey()
	if err != nil {
		return nil, err
	}
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      key,
				Operator: metav1.LabelSelectorOpExists,
			},
		},
	}

	return labelSelector, nil
}

// ComponentSelector returns a Selector that matches this operator's component label.
func (o *Operator) ComponentSelector() (labels.Selector, error) {
	labelSelector, err := o.ComponentLabelSelector()
	if err != nil {
		return nil, err
	}

	return metav1.LabelSelectorAsSelector(labelSelector)
}

// ResetComponents resets the component selector and references in the operator's status.
func (o *Operator) ResetComponents() error {
	labelSelector, err := o.ComponentLabelSelector()
	if err != nil {
		return err
	}

	o.Status.Components = &operatorsv2alpha1.Components{
		LabelSelector: labelSelector,
	}

	return nil
}

// AddComponents adds the given components to the operator's status and returns an error
// if a component isn't associated with the operator by label.
// List type arguments are flattened to their nested elements before being added.
func (o *Operator) AddComponents(components ...runtime.Object) error {
	selector, err := o.ComponentSelector()
	if err != nil {
		return err
	}

	var refs []operatorsv2alpha1.Ref
	for _, component := range components {
		// Unpack nested components
		if nested, err := meta.ExtractList(component); err == nil {
			if err = o.AddComponents(nested...); err != nil {
				return err
			}

			continue
		}

		m, err := meta.Accessor(component)
		if err != nil {
			return err
		}

		t, err := meta.TypeAccessor(component)
		if err != nil {
			return err
		}

		if !selector.Matches(labels.Set(m.GetLabels())) {
			return fmt.Errorf("Cannot add component %s/%s/%s to Operator %s: component labels not selected by %s", t.GetKind(), m.GetNamespace(), m.GetName(), o.GetName(), selector.String())
		}

		ref, err := reference.GetReference(componentScheme, component)
		if err != nil {
			return err
		}

		componentRef := operatorsv2alpha1.Ref{
			ObjectReference: ref,
		}
		refs = append(refs, componentRef)
	}

	if o.Status.Components == nil {
		if err := o.ResetComponents(); err != nil {
			return err
		}
	}

	o.Status.Components.Refs = append(o.Status.Components.Refs, refs...)

	return nil
}

// SetComponents sets the component references in the operator's status to the given components.
func (o *Operator) SetComponents(components ...runtime.Object) error {
	if err := o.ResetComponents(); err != nil {
		return err
	}

	return o.AddComponents(components...)
}
