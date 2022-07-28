package errors

import "fmt"

// MultipleExistingCRDOwnersError is an error that denotes multiple owners of a CRD exist
// simultaneously in the same namespace
type MultipleExistingCRDOwnersError struct {
	CSVNames  []string
	CRDName   string
	Namespace string
}

type UnadoptableError struct {
	resourceNamespace string
	resourceName      string
}

func (err UnadoptableError) Error() string {
	if err.resourceNamespace == "" {
		return fmt.Sprintf("%s is unadoptable", err.resourceName)
	}
	return fmt.Sprintf("%s/%s is unadoptable", err.resourceNamespace, err.resourceName)
}

func NewUnadoptableError(resourceNamespace, resourceName string) UnadoptableError {
	return UnadoptableError{resourceNamespace, resourceName}
}

func (m MultipleExistingCRDOwnersError) Error() string {
	return fmt.Sprintf("Existing CSVs %v in namespace %s all claim to own CRD %s", m.CSVNames, m.Namespace, m.CRDName)
}

func NewMultipleExistingCRDOwnersError(csvNames []string, crdName string, namespace string) MultipleExistingCRDOwnersError {
	return MultipleExistingCRDOwnersError{
		CSVNames:  csvNames,
		CRDName:   crdName,
		Namespace: namespace,
	}
}

func IsMultipleExistingCRDOwnersError(err error) bool {
	switch err.(type) {
	case MultipleExistingCRDOwnersError:
		return true
	}

	return false
}

type FatalError struct {
	error
}

func NewFatalError(err error) FatalError {
	return FatalError{err}
}
func IsFatal(err error) bool {
	switch err.(type) {
	case FatalError:
		return true
	}
	return false
}

// GroupVersionKindNotFoundError occurs when we can't find an API via discovery
type GroupVersionKindNotFoundError struct {
	Group   string
	Version string
	Kind    string
}

func (g GroupVersionKindNotFoundError) Error() string {
	return fmt.Sprintf("Unable to find GVK in discovery: %s %s %s", g.Group, g.Version, g.Kind)
}
