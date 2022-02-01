/*
Copyright 2020 The Operator-SDK Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package updater

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	olmv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/api/v1alpha1"
)

func New(client client.Client) Updater {
	return Updater{
		client: client,
	}
}

type Updater struct {
	client            client.Client
	updateStatusFuncs []UpdateStatusFunc
}

type UpdateStatusFunc func(bundle *olmv1alpha1.BundleStatus) bool

func (u *Updater) UpdateStatus(fs ...UpdateStatusFunc) {
	u.updateStatusFuncs = append(u.updateStatusFuncs, fs...)
}

func (u *Updater) Apply(ctx context.Context, b *olmv1alpha1.Bundle) error {
	backoff := retry.DefaultRetry

	return retry.RetryOnConflict(backoff, func() error {
		if err := u.client.Get(ctx, client.ObjectKeyFromObject(b), b); err != nil {
			return err
		}
		needsStatusUpdate := false
		for _, f := range u.updateStatusFuncs {
			needsStatusUpdate = f(&b.Status) || needsStatusUpdate
		}
		if needsStatusUpdate {
			log.FromContext(ctx).Info("applying status changes")
			return u.client.Status().Update(ctx, b)
		}
		return nil
	})
}

func EnsureCondition(condition metav1.Condition) UpdateStatusFunc {
	return func(status *olmv1alpha1.BundleStatus) bool {
		existing := meta.FindStatusCondition(status.Conditions, condition.Type)
		meta.SetStatusCondition(&status.Conditions, condition)
		return existing == nil || !conditionsSemanticallyEqual(*existing, condition)
	}
}

func conditionsSemanticallyEqual(a, b metav1.Condition) bool {
	return a.Type == b.Type && a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message && a.ObservedGeneration == b.ObservedGeneration
}

func EnsureObservedGeneration(observedGeneration int64) UpdateStatusFunc {
	return func(status *olmv1alpha1.BundleStatus) bool {
		if status.ObservedGeneration == observedGeneration {
			return false
		}
		status.ObservedGeneration = observedGeneration
		return true
	}
}

func EnsureBundleDigest(digest string) UpdateStatusFunc {
	return func(status *olmv1alpha1.BundleStatus) bool {
		if status.Digest == digest {
			return false
		}
		status.Digest = digest
		return true
	}
}

func UnsetBundleInfo() UpdateStatusFunc {
	return SetBundleInfo(nil)
}

func SetBundleInfo(info *olmv1alpha1.BundleInfo) UpdateStatusFunc {
	return func(status *olmv1alpha1.BundleStatus) bool {
		if reflect.DeepEqual(status.Info, info) {
			return false
		}
		status.Info = info
		return true
	}
}

func SetPhase(phase string) UpdateStatusFunc {
	return func(status *olmv1alpha1.BundleStatus) bool {
		if status.Phase == phase {
			return false
		}
		status.Phase = phase
		return true
	}
}
