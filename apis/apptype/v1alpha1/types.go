package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	AppTypeCRDName       = "apptype-v1s.app.coreos.com"
	AppTypeCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support
)

// AppTypeSpec defines an Application that can be installed
type AppTypeSpec struct {
	DisplayName string       `json:"displayName"`
	Description string       `json:"description"`
	Keywords    []string     `json:"keywords"`
	Maintainers []Maintainer `json:"maintainers"`
	Links       []AppLink    `json:"links"`
	Icon        Icon         `json:"iconURL"`
}

type Maintainer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type AppLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Icon struct {
	Data      string `json:"base64data"`
	MediaType string `json:"mediatype"`
}

// Custom Resource of type "AppTypeSpec" (AppTypeSpec CRD created by ALM)
type AppType struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *AppTypeSpec  `json:"spec"`
	Status metav1.Status `json:"status"`
}

func NewAppTypeResource(app *AppTypeSpec) *AppType {
	resource := AppType{}
	resource.Kind = AppTypeCRDName
	resource.APIVersion = AppTypeCRDAPIVersion
	resource.Spec = app
	return &resource
}

type AppTypeList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*AppTypeSpec `json:"items"`
}
