package containertools

type GetImageDataOptions struct {
	WorkingDir string
}

type GetImageDataOption func(*GetImageDataOptions)

func WithWorkingDir(workingDir string) GetImageDataOption {
	return func(o *GetImageDataOptions) {
		o.WorkingDir = workingDir
	}
}
