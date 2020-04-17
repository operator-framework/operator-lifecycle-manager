package decorators

import (
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
	operatorsv2alpha1 "github.com/operator-framework/api/pkg/operators/v2alpha1"
	"github.com/yalp/jsonpath"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/reference"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/codec"
)

const (
	newOperatorError            = "Cannot create new Operator: %s"
	newComponentError           = "Cannot create new Component: %s"
	componentLabelKeyError      = "Cannot generate component label key: %s"
	componentConditionsJSONPath = "$.status.conditions"

	// ComponentLabelKeyPrefix is the key prefix used for labels marking operator component resources.
	ComponentLabelKeyPrefix = "operators.coreos.com/"
)

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

type OperatorFactory interface {
	// NewOperator returns an Operator decorator that wraps the given external Operator representation.
	// An error is returned if the decorator cannon be instantiated.
	NewOperator(external *operatorsv2alpha1.Operator) (*Operator, error)
}

// schemedOperatorFactory is an OperatorFactory that instantiates Operator decorators with a shared scheme.
type schemedOperatorFactory struct {
	scheme *runtime.Scheme
}

func (s *schemedOperatorFactory) NewOperator(external *operatorsv2alpha1.Operator) (*Operator, error) {
	if external == nil {
		return nil, fmt.Errorf(newOperatorError, "cannot create operator with nil external type")
	}

	return &Operator{
		Operator: external.DeepCopy(),
		scheme:   s.scheme,
	}, nil
}

// NewSchemedOperatorFactory returns an OperatorFactory that supplies a scheme to all Operators it creates.
func NewSchemedOperatorFactory(scheme *runtime.Scheme) (OperatorFactory, error) {
	if scheme == nil {
		return nil, fmt.Errorf("cannot create factory with nil scheme")
	}

	return &schemedOperatorFactory{
		scheme: scheme,
	}, nil
}

// Operator decorates an external Operator and provides convenience methods for managing it.
type Operator struct {
	*operatorsv2alpha1.Operator

	scheme *runtime.Scheme
}

// ComponentLabelKey returns the operator's completed component label key
func (a *Operator) ComponentLabelKey() (string, error) {
	if a.GetName() == "" {
		return "", fmt.Errorf(componentLabelKeyError, "empty name field")
	}

	return ComponentLabelKeyPrefix + a.GetName(), nil
}

// ComponentLabelSelector returns a LabelSelector that matches this operator's component label.
func (a *Operator) ComponentLabelSelector() (*metav1.LabelSelector, error) {
	key, err := a.ComponentLabelKey()
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
func (a *Operator) ComponentSelector() (labels.Selector, error) {
	labelSelector, err := a.ComponentLabelSelector()
	if err != nil {
		return nil, err
	}

	return metav1.LabelSelectorAsSelector(labelSelector)
}

// ResetComponents resets the component selector and references in the operator's status.
func (a *Operator) ResetComponents() error {
	labelSelector, err := a.ComponentLabelSelector()
	if err != nil {
		return err
	}

	a.Status.Components = &operatorsv2alpha1.Components{
		LabelSelector: labelSelector,
	}

	return nil
}

// AddComponents adds the given components to the operator's status and returns an error
// if a component isn't associated with the operator by label.
// List type arguments are flattened to their nested elements before being added.
func (a *Operator) AddComponents(components ...runtime.Object) error {
	selector, err := a.ComponentSelector()
	if err != nil {
		return err
	}

	var refs []operatorsv2alpha1.RichReference
	for _, obj := range components {
		// Unpack nested components
		if nested, err := meta.ExtractList(obj); err == nil {
			if err = a.AddComponents(nested...); err != nil {
				return err
			}

			continue
		}

		component, err := NewComponent(obj, a.scheme)
		if err != nil {
			return err
		}
		if matches, err := component.Matches(selector); err != nil {
			return err
		} else if !matches {
			return fmt.Errorf("Cannot add component %s/%s/%s to Operator %s: component labels not selected by %s", component.GetKind(), component.GetNamespace(), component.GetName(), a.GetName(), selector.String())
		}

		ref, err := component.Reference()
		if err != nil {
			return err
		}
		refs = append(refs, *ref)
	}

	if a.Status.Components == nil {
		if err := a.ResetComponents(); err != nil {
			return err
		}
	}

	a.Status.Components.Refs = append(a.Status.Components.Refs, refs...)

	return nil
}

// SetComponents sets the component references in the operator's status to the given components.
func (a *Operator) SetComponents(components ...runtime.Object) error {
	if err := a.ResetComponents(); err != nil {
		return err
	}

	return a.AddComponents(components...)
}

type Component struct {
	*unstructured.Unstructured

	scheme *runtime.Scheme
}

// NewComponent returns a new Component instance.
func NewComponent(component runtime.Object, scheme *runtime.Scheme) (*Component, error) {
	if component == nil {
		return nil, fmt.Errorf(newComponentError, "nil component")
	}

	if scheme == nil {
		return nil, fmt.Errorf(newComponentError, "nil scheme")
	}

	u := &unstructured.Unstructured{}
	if err := scheme.Convert(component, u, nil); err != nil {
		return nil, err
	}

	c := &Component{
		Unstructured: u,
		scheme:       scheme,
	}

	return c, nil
}

func (c *Component) Matches(selector labels.Selector) (matches bool, err error) {
	m, err := meta.Accessor(c)
	if err != nil {
		return
	}
	matches = selector.Matches(labels.Set(m.GetLabels()))

	return
}

func (c *Component) Reference() (ref *operatorsv2alpha1.RichReference, err error) {
	truncated, err := c.truncatedReference()
	if err != nil {
		return
	}
	ref = &operatorsv2alpha1.RichReference{
		ObjectReference: truncated,
	}

	out, _ := jsonpath.Read(c.UnstructuredContent(), componentConditionsJSONPath)
	if out == nil {
		return
	}

	var decoder *mapstructure.Decoder
	decoder, err = mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Metadata:   nil,
		DecodeHook: codec.MetaTimeHookFunc(),
		Result:     &ref.Conditions,
	})
	if err != nil {
		return
	}

	err = decoder.Decode(out)

	return
}

func (c *Component) truncatedReference() (ref *corev1.ObjectReference, err error) {
	ref, err = reference.GetReference(c.scheme, c.Unstructured)
	if err != nil {
		return
	}

	ref = &corev1.ObjectReference{
		Kind:       ref.Kind,
		APIVersion: ref.APIVersion,
		Namespace:  ref.Namespace,
		Name:       ref.Name,
	}

	return
}
