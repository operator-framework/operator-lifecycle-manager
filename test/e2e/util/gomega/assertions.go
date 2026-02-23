package gomega

import (
	"context"

	. "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/util"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func EventuallyResource(actual k8scontrollerclient.Object, intervals ...interface{}) AsyncAssertion {
	client := ctx.Ctx().Client()
	clone := actual.DeepCopyObject().(k8scontrollerclient.Object)
	key := k8scontrollerclient.ObjectKeyFromObject(actual)
	getObjectFn := func() k8scontrollerclient.Object {
		if err := client.Get(context.Background(), key, clone); err != nil {
			util.Logf("ERROR getting resource '%s'", util.ObjectToJsonString(key), err)
			return nil
		}
		return clone
	}
	return Eventually(getObjectFn, intervals...)
}
