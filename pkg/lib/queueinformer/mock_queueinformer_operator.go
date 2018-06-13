package queueinformer

import (
	"github.com/golang/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// MockListWatcher mocks k8s.io/client-go/tools/cache.ListerWatcher.
//
// This is type is useful for mocking Informers.
type MockListWatcher struct{}

// List always returns (nil, nil).
func (l *MockListWatcher) List(options metav1.ListOptions) (runtime.Object, error) {
	return nil, nil
}

// Watch always returns (nil, nil).
func (l *MockListWatcher) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return nil, nil
}

// MockOperator uses TestQueueinformers and a Mock operator client
type MockOperator struct {
	Operator
	testQueueInformers []*TestQueueInformer
	MockClient         *operatorclient.MockClientInterface
}

// NewMockOperator creates a new Operator configured to manage the cluster defined in kubeconfig.
func NewMockOperator(gomockCtrl *gomock.Controller, testQueueInformers ...*TestQueueInformer) *MockOperator {
	mockClient := operatorclient.NewMockClientInterface(gomockCtrl)

	if testQueueInformers == nil {
		testQueueInformers = []*TestQueueInformer{}
	}
	queueInformers := []*QueueInformer{}
	for _, informer := range testQueueInformers {
		queueInformers = append(queueInformers, &informer.QueueInformer)
	}
	operator := &MockOperator{
		Operator: Operator{
			queueInformers: queueInformers,
			OpClient:       mockClient,
		},
		testQueueInformers: testQueueInformers,
		MockClient:         mockClient,
	}
	return operator
}

// RegisterQueueInformer adds a QueueInformer to this operator
func (o *MockOperator) RegisterQueueInformer(queueInformer *QueueInformer) {
	o.testQueueInformers = append(o.testQueueInformers, &TestQueueInformer{*queueInformer})
	o.Operator.queueInformers = append(o.queueInformers, queueInformer)
}
