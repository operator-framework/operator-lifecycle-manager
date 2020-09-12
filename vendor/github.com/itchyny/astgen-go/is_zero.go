package astgen

import "reflect"

func isZero(val reflect.Value) bool {
	switch val.Kind() {
	case reflect.Bool:
		return !val.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return val.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return val.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return val.Float() == 0
	case reflect.Complex64, reflect.Complex128:
		return val.Complex() == 0
	case reflect.Array:
		for i := 0; i < val.Len(); i++ {
			if !isZero(val.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map, reflect.Slice, reflect.String:
		return val.Len() == 0
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Ptr:
		return val.IsNil()
	case reflect.Struct:
		for i := 0; i < val.NumField(); i++ {
			if !isZero(val.Field(i)) {
				return false
			}
		}
		return true
	}
	panic("unknown type: " + val.Type().String())
}
