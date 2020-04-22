package codec

import (
	"reflect"
	"time"

	"github.com/mitchellh/mapstructure"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func MetaTimeHookFunc() mapstructure.DecodeHookFunc {
	return metaTimeHookFunc
}

func metaTimeHookFunc(f, t reflect.Type, data interface{}) (interface{}, error) {
	if t != reflect.TypeOf(metav1.Time{}) {
		return data, nil
	}

	var (
		tm  time.Time
		err error
	)
	switch f.Kind() {
	case reflect.String:
		tm, err = time.Parse(time.RFC3339, data.(string))
	case reflect.Float64:
		tm = time.Unix(0, int64(data.(float64))*int64(time.Millisecond))
	case reflect.Int64:
		tm = time.Unix(0, data.(int64)*int64(time.Millisecond))
	default:
		return data, nil
	}

	if err != nil {
		return nil, err
	}

	return metav1.NewTime(tm), nil
}
