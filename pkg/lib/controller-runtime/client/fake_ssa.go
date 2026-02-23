package client

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakecontrollerclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// FakeApplier provides a wrapper around the fake k8s controller client to convert the unsupported apply-type patches to merge patches.
func NewFakeApplier(scheme *runtime.Scheme, owner string, objs ...runtime.Object) *ServerSideApplier {
	return &ServerSideApplier{
		client: &fakeApplier{fakecontrollerclient.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()},
		Scheme: scheme,
		Owner:  k8scontrollerclient.FieldOwner(owner),
	}
}

type fakeApplier struct {
	k8scontrollerclient.Client
}

func (c *fakeApplier) Patch(ctx context.Context, obj k8scontrollerclient.Object, patch k8scontrollerclient.Patch, opts ...k8scontrollerclient.PatchOption) error {
	patch, opts = convertApplyToMergePatch(patch, opts...)
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *fakeApplier) Status() k8scontrollerclient.StatusWriter {
	return fakeStatusWriter{c.Client.Status()}
}

type fakeStatusWriter struct {
	k8scontrollerclient.StatusWriter
}

func (c fakeStatusWriter) Patch(ctx context.Context, obj k8scontrollerclient.Object, patch k8scontrollerclient.Patch, opts ...k8scontrollerclient.SubResourcePatchOption) error {
	// Apply patch type is not supported on the fake controller
	// Convert Apply patches to Merge patches
	if patch.Type() == types.ApplyPatchType {
		patch = k8scontrollerclient.Merge
		// Filter out ForceOwnership option since it's only valid for Apply patches
		subResourceOpts := make([]k8scontrollerclient.SubResourcePatchOption, 0, len(opts))
		for _, opt := range opts {
			// Skip ForceOwnership options by checking if they would apply it
			if subOpt, ok := opt.(interface{ ApplyToPatch(*k8scontrollerclient.PatchOptions) }); ok {
				testOpts := &k8scontrollerclient.PatchOptions{}
				subOpt.ApplyToPatch(testOpts)
				// If Force is set, this is ForceOwnership, skip it
				if testOpts.Force != nil && *testOpts.Force {
					continue
				}
			}
			subResourceOpts = append(subResourceOpts, opt)
		}
		opts = subResourceOpts
	}
	return c.StatusWriter.Patch(ctx, obj, patch, opts...)
}

func convertApplyToMergePatch(patch k8scontrollerclient.Patch, opts ...k8scontrollerclient.PatchOption) (k8scontrollerclient.Patch, []k8scontrollerclient.PatchOption) {
	// Apply patch type is not supported on the fake controller
	if patch.Type() == types.ApplyPatchType {
		patch = k8scontrollerclient.Merge
		patchOptions := make([]k8scontrollerclient.PatchOption, 0, len(opts))
		for _, opt := range opts {
			if opt == k8scontrollerclient.ForceOwnership {
				continue
			}
			patchOptions = append(patchOptions, opt)
		}
		opts = patchOptions
	}
	return patch, opts
}
