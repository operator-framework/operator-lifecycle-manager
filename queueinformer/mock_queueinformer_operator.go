package queueinformer

import (
	opClient "github.com/coreos-inc/operator-client/pkg/client"
	"github.com/golang/mock/gomock"
)

// MockOperator uses TestQueueinformers and a Mock operator client
type MockOperator struct {
	Operator
	testQueueInformers []*TestQueueInformer
}

// NewMockOperator creates a new Operator configured to manage the cluster defined in kubeconfig.
func NewMockOperator(gomockCtrl *gomock.Controller, testQueueInformers ...*TestQueueInformer) *MockOperator {
	mockClient := opClient.NewMockInterface(gomockCtrl)

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
	}
	return operator
}

// RegisterQueueInformer adds a QueueInformer to this operator
func (o *MockOperator) RegisterQueueInformer(queueInformer *QueueInformer) {
	o.testQueueInformers = append(o.testQueueInformers, &TestQueueInformer{*queueInformer})
	o.Operator.queueInformers = append(o.queueInformers, queueInformer)
}
