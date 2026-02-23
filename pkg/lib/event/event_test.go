package event

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

func TestSafeSpamKeyFunc(t *testing.T) {
	tests := []struct {
		name     string
		event    *v1.Event
		expected string
	}{
		{
			name:     "nil event",
			event:    nil,
			expected: "unknown/unknown/unknown/unknown",
		},
		{
			name: "empty event",
			event: &v1.Event{
				InvolvedObject: v1.ObjectReference{},
			},
			expected: "Unknown//unknown/",
		},
		{
			name: "valid event",
			event: &v1.Event{
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "test-pod",
				},
				Reason: "Created",
			},
			expected: "Pod/default/test-pod/Created",
		},
		{
			name: "event with empty kind",
			event: &v1.Event{
				InvolvedObject: v1.ObjectReference{
					Namespace: "default",
					Name:      "test-pod",
				},
				Reason: "Created",
			},
			expected: "Unknown/default/test-pod/Created",
		},
		{
			name: "event with empty name",
			event: &v1.Event{
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Namespace: "default",
				},
				Reason: "Created",
			},
			expected: "Pod/default/unknown/Created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := safeSpamKeyFunc(tt.event)
			if result != tt.expected {
				t.Errorf("safeSpamKeyFunc() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestIsValidObject(t *testing.T) {
	tests := []struct {
		name     string
		object   runtime.Object
		expected bool
	}{
		{
			name:     "nil object",
			object:   nil,
			expected: false,
		},
		{
			name: "valid pod",
			object: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			expected: true,
		},
		{
			name: "pod with empty name",
			object: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
				},
			},
			expected: false,
		},
		{
			name: "valid namespace",
			object: &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns",
				},
			},
			expected: true,
		},
		{
			name: "valid ObjectReference",
			object: &v1.ObjectReference{
				Kind:      "InstallPlan",
				Namespace: "default",
				Name:      "test-plan",
				FieldPath: "status.plan[0]",
			},
			expected: true,
		},
		{
			name: "ObjectReference with empty name",
			object: &v1.ObjectReference{
				Kind:      "InstallPlan",
				Namespace: "default",
				FieldPath: "status.plan[0]",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidObject(tt.object)
			if result != tt.expected {
				t.Errorf("isValidObject() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// mockEventRecorder is a mock implementation of record.EventRecorder for testing
type mockEventRecorder struct {
	events []string
}

func (m *mockEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	m.events = append(m.events, reason+":"+message)
}

func (m *mockEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	m.events = append(m.events, reason+":"+messageFmt)
}

func (m *mockEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	m.events = append(m.events, reason+":"+messageFmt)
}

// Ensure mockEventRecorder implements record.EventRecorder
var _ record.EventRecorder = &mockEventRecorder{}

func TestSafeEventRecorder_Event(t *testing.T) {
	tests := []struct {
		name           string
		object         runtime.Object
		expectRecorded bool
	}{
		{
			name:           "nil object - should not record",
			object:         nil,
			expectRecorded: false,
		},
		{
			name: "valid object - should record",
			object: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			expectRecorded: true,
		},
		{
			name: "object with empty name - should not record",
			object: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
				},
			},
			expectRecorded: false,
		},
		{
			name: "valid ObjectReference - should record",
			object: &v1.ObjectReference{
				Kind:      "InstallPlan",
				Namespace: "default",
				Name:      "test-plan",
				FieldPath: "status.plan[0]",
			},
			expectRecorded: true,
		},
		{
			name: "ObjectReference with empty name - should not record",
			object: &v1.ObjectReference{
				Kind:      "InstallPlan",
				Namespace: "default",
			},
			expectRecorded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockEventRecorder{}
			safe := &SafeEventRecorder{recorder: mock}

			safe.Event(tt.object, v1.EventTypeNormal, "TestReason", "Test message")

			if tt.expectRecorded && len(mock.events) != 1 {
				t.Errorf("Expected event to be recorded, but got %d events", len(mock.events))
			}
			if !tt.expectRecorded && len(mock.events) != 0 {
				t.Errorf("Expected no events to be recorded, but got %d events", len(mock.events))
			}
		})
	}
}

func TestSafeEventRecorder_Eventf(t *testing.T) {
	tests := []struct {
		name           string
		object         runtime.Object
		expectRecorded bool
	}{
		{
			name:           "nil object - should not record",
			object:         nil,
			expectRecorded: false,
		},
		{
			name: "valid object - should record",
			object: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			expectRecorded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockEventRecorder{}
			safe := &SafeEventRecorder{recorder: mock}

			safe.Eventf(tt.object, v1.EventTypeNormal, "TestReason", "Test message %s", "arg")

			if tt.expectRecorded && len(mock.events) != 1 {
				t.Errorf("Expected event to be recorded, but got %d events", len(mock.events))
			}
			if !tt.expectRecorded && len(mock.events) != 0 {
				t.Errorf("Expected no events to be recorded, but got %d events", len(mock.events))
			}
		})
	}
}

func TestSafeEventRecorder_AnnotatedEventf(t *testing.T) {
	tests := []struct {
		name           string
		object         runtime.Object
		expectRecorded bool
	}{
		{
			name:           "nil object - should not record",
			object:         nil,
			expectRecorded: false,
		},
		{
			name: "valid object - should record",
			object: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
				},
			},
			expectRecorded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockEventRecorder{}
			safe := &SafeEventRecorder{recorder: mock}

			annotations := map[string]string{"key": "value"}
			safe.AnnotatedEventf(tt.object, annotations, v1.EventTypeNormal, "TestReason", "Test message %s", "arg")

			if tt.expectRecorded && len(mock.events) != 1 {
				t.Errorf("Expected event to be recorded, but got %d events", len(mock.events))
			}
			if !tt.expectRecorded && len(mock.events) != 0 {
				t.Errorf("Expected no events to be recorded, but got %d events", len(mock.events))
			}
		})
	}
}
