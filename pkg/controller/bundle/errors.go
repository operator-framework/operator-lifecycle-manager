package bundle

// bundleJobError is an error type returned by UnpackBundle() when the
// bundle unpack job fails (e.g due to a timeout)
type bundleJobError struct {
	s string
}

func NewBundleJobError(s string) error {
	return bundleJobError{s: s}
}

func (e bundleJobError) Error() string {
	return e.s
}

func (e bundleJobError) IsBundleJobError() bool {
	return true
}

// IsBundleJobError checks if an error is an error due to the bundle extract job failing.
func IsBundleJobError(err error) bool {
	type bundleJobError interface {
		IsBundleJobError() bool
	}
	ogErr, ok := err.(bundleJobError)
	return ok && ogErr.IsBundleJobError()
}
