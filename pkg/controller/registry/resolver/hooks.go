package resolver

// stepResolverInitHook provides a way for the downstream
// to modify the step resolver at creation time.
// This is a bit of a hack to enable system constraints downstream
// without affecting the upstream. We may want to clean this up when
// either we have a more pluggable architecture; or system constraints
// come to the upstream
type stepResolverInitHook func(*OperatorStepResolver) error
