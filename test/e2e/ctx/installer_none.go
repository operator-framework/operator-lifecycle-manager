//go:build !helm
// +build !helm

package ctx

func Install(ctx *TestContext) error {
	return nil
}
