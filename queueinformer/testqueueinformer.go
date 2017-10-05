package queueinformer

// TestQueueInformer wraps a normal queueinformer with knobs for injecting data for testing
type TestQueueInformer struct {
	QueueInformer
}

func (q *TestQueueInformer) Enqueue(obj interface{}) {
	q.QueueInformer.enqueue(obj)
}
