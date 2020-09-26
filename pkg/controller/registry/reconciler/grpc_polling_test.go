package reconciler

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

func TestSyncRegistryUpdateInterval(t *testing.T) {
	tests := []struct {
		name     string
		source   *v1alpha1.CatalogSource
		expected time.Duration
	}{
		{
			name: "PollingInterval10Minutes/FirstUpdate",
			source: &v1alpha1.CatalogSource{
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{
							Interval: &metav1.Duration{
								Duration: 10 * time.Minute,
							},
						},
					},
				},
			},
			expected: 10 * time.Minute,
		},
		{
			name: "PollingInterval15Minutes/FirstUpdate",
			source: &v1alpha1.CatalogSource{
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{
							Interval: &metav1.Duration{
								Duration: 15 * time.Minute,
							},
						},
					},
				},
			},
			expected: 15 * time.Minute,
		},
		{
			name: "PollingInterval10Minutes/AlreadyUpdated",
			source: &v1alpha1.CatalogSource{
				Status: v1alpha1.CatalogSourceStatus{
					LatestImageRegistryPoll: &metav1.Time{
						time.Now().Add(-(5 * time.Minute)),
					},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{
							Interval: &metav1.Duration{
								Duration: 10 * time.Minute,
							},
						},
					},
				},
			},
			expected: 10 * time.Minute,
		},
		{
			name: "PollingInterval40Minutes/FirstUpdate",
			source: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-(35 * time.Minute)),
					},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{
							Interval: &metav1.Duration{
								Duration: 40 * time.Minute,
							},
						},
					},
				},
			},
			expected: 10 * time.Minute,
		},
		{
			name: "PollingInterval40Minutes/AlreadyUpdated30MinutesAgo",
			source: &v1alpha1.CatalogSource{
				Status: v1alpha1.CatalogSourceStatus{
					LatestImageRegistryPoll: &metav1.Time{
						time.Now().Add(-(30 * time.Minute)),
					},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{
							Interval: &metav1.Duration{
								Duration: 40 * time.Minute,
							},
						},
					},
				},
			},
			expected: 10 * time.Minute,
		},
		{
			name: "PollingInterval1hour/FirstUpdate",
			source: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{
						Time: time.Now().Add(-(15 * time.Minute)),
					},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{
							Interval: &metav1.Duration{
								Duration: 1 * time.Hour,
							},
						},
					},
				},
			},
			expected: queueinformer.DefaultResyncPeriod,
		},
		{
			name: "PollingInterval10Hours/AlreadyUpdated",
			source: &v1alpha1.CatalogSource{
				Status: v1alpha1.CatalogSourceStatus{
					LatestImageRegistryPoll: &metav1.Time{
						time.Now().Add(-(15 * time.Minute)),
					},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{
							Interval: &metav1.Duration{
								Duration: 10 * time.Hour,
							},
						},
					},
				},
			},
			expected: queueinformer.DefaultResyncPeriod,
		},
	}

	for _, tt := range tests {
		t.Logf("name %s", tt.name)
		d := SyncRegistryUpdateInterval(tt.source)
		if d != tt.expected {
			t.Fatalf("unexpected registry sync interval for %s: expected %#v got %#v", tt.name, tt.expected, d)
		}
	}
}
