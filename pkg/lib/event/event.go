package event

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/scheme"
)

const component string = "operator-lifecycle-manager"

var s = scheme.Scheme

func init() {
	if err := kscheme.AddToScheme(s); err != nil {
		panic(err)
	}
}

// safeSpamKeyFunc builds a spam key from event fields with nil checks to prevent panics.
// This protects against nil pointer dereferences when event.InvolvedObject fields are empty.
func safeSpamKeyFunc(event *v1.Event) string {
	if event == nil {
		return "unknown/unknown/unknown/unknown"
	}

	kind := event.InvolvedObject.Kind
	namespace := event.InvolvedObject.Namespace
	name := event.InvolvedObject.Name
	reason := event.Reason

	// Provide defaults for empty fields to avoid issues
	if kind == "" {
		kind = "Unknown"
	}
	if name == "" {
		name = "unknown"
	}

	return fmt.Sprintf("%s/%s/%s/%s", kind, namespace, name, reason)
}

// SafeEventRecorder wraps record.EventRecorder with nil checks to prevent panics
// when recording events for objects with nil or invalid metadata.
type SafeEventRecorder struct {
	recorder record.EventRecorder
}

// isValidObject checks if the object has valid metadata required for event recording.
func isValidObject(object runtime.Object) bool {
	if object == nil {
		return false
	}

	// Handle ObjectReference type (used for events with FieldPath)
	if ref, ok := object.(*v1.ObjectReference); ok {
		return ref.Name != ""
	}

	// Check if object implements metav1.Object interface
	accessor, ok := object.(metav1.Object)
	if !ok {
		return false
	}

	// Ensure the object has a valid name (required for event recording)
	if accessor.GetName() == "" {
		return false
	}

	return true
}

// Event records an event for the given object, with nil checks.
func (s *SafeEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	if !isValidObject(object) {
		klog.V(4).Infof("Skipping event recording: invalid object (nil or missing name), reason=%s, message=%s", reason, message)
		return
	}
	s.recorder.Event(object, eventtype, reason, message)
}

// Eventf records a formatted event for the given object, with nil checks.
func (s *SafeEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	if !isValidObject(object) {
		klog.V(4).Infof("Skipping event recording: invalid object (nil or missing name), reason=%s, messageFmt=%s", reason, messageFmt)
		return
	}
	s.recorder.Eventf(object, eventtype, reason, messageFmt, args...)
}

// AnnotatedEventf records a formatted event with annotations for the given object, with nil checks.
func (s *SafeEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	if !isValidObject(object) {
		klog.V(4).Infof("Skipping event recording: invalid object (nil or missing name), reason=%s, messageFmt=%s", reason, messageFmt)
		return
	}
	s.recorder.AnnotatedEventf(object, annotations, eventtype, reason, messageFmt, args...)
}

// NewRecorder returns an EventRecorder type that can be
// used to post Events to different object's lifecycles.
// The returned recorder includes nil checks to prevent panics from invalid objects.
func NewRecorder(event typedcorev1.EventInterface) (record.EventRecorder, error) {
	eventBroadcaster := record.NewBroadcasterWithCorrelatorOptions(record.CorrelatorOptions{
		BurstSize:   10,
		SpamKeyFunc: safeSpamKeyFunc,
	})
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: event})
	recorder := eventBroadcaster.NewRecorder(s, v1.EventSource{Component: component})

	// Wrap the recorder with SafeEventRecorder for nil protection
	return &SafeEventRecorder{recorder: recorder}, nil
}
