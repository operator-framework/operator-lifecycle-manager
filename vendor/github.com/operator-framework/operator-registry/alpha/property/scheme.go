package property

import (
	"fmt"
	"reflect"
)

func init() {
	scheme = map[reflect.Type]string{
		reflect.TypeOf(&Package{}):         TypePackage,
		reflect.TypeOf(&PackageRequired{}): TypePackageRequired,
		reflect.TypeOf(&GVK{}):             TypeGVK,
		reflect.TypeOf(&GVKRequired{}):     TypeGVKRequired,
		reflect.TypeOf(&BundleObject{}):    TypeBundleObject,
	}
}

var scheme map[reflect.Type]string

func AddToScheme(typ string, p interface{}) {
	t := reflect.TypeOf(p)
	if t.Kind() != reflect.Ptr {
		panic("input must be a pointer to a type")
	}
	if _, ok := scheme[t]; ok {
		panic(fmt.Sprintf("scheme already contains registration for type %q", t))
	}
	scheme[t] = typ
}
