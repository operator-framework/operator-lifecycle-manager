package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorv1clientset "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	v1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-registry/pkg/api"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type csvSourceProvider struct {
	csvLister v1alpha1listers.ClusterServiceVersionLister
	subLister v1alpha1listers.SubscriptionLister
	ogLister  v1listers.OperatorGroupLister
	logger    logrus.StdLogger
	client    operatorv1clientset.Interface
}

func (csp *csvSourceProvider) Sources(namespaces ...string) map[cache.SourceKey]cache.Source {
	result := make(map[cache.SourceKey]cache.Source)
	for _, namespace := range namespaces {
		result[cache.NewVirtualSourceKey(namespace)] = &csvSource{
			key:       cache.NewVirtualSourceKey(namespace),
			csvLister: csp.csvLister.ClusterServiceVersions(namespace),
			subLister: csp.subLister.Subscriptions(namespace),
			ogLister:  csp.ogLister.OperatorGroups(namespace),
			logger:    csp.logger,
			listSubscriptions: func(ctx context.Context) (*v1alpha1.SubscriptionList, error) {
				return csp.client.OperatorsV1alpha1().Subscriptions(namespace).List(ctx, metav1.ListOptions{})
			},
		}
		break // first ns is assumed to be the target ns, todo: make explicit
	}
	return result
}

type csvSource struct {
	key       cache.SourceKey
	csvLister v1alpha1listers.ClusterServiceVersionNamespaceLister
	subLister v1alpha1listers.SubscriptionNamespaceLister
	ogLister  v1listers.OperatorGroupNamespaceLister
	logger    logrus.StdLogger

	listSubscriptions func(context.Context) (*v1alpha1.SubscriptionList, error)
}

func (s *csvSource) Snapshot(ctx context.Context) (*cache.Snapshot, error) {
	csvs, err := s.csvLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	subs, err := s.subLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	failForwardEnabled, err := IsFailForwardEnabled(s.ogLister)
	if err != nil {
		return nil, err
	}

	// build a catalog snapshot of CSVs without subscriptions
	csvSubscriptions := make(map[*v1alpha1.ClusterServiceVersion]*v1alpha1.Subscription)
	for _, sub := range subs {
		for _, csv := range csvs {
			if csv.IsCopied() {
				continue
			}
			if csv.Name == sub.Status.InstalledCSV {
				csvSubscriptions[csv] = sub
				break
			}
		}
	}

	var csvsMissingProperties []*v1alpha1.ClusterServiceVersion
	var entries []*cache.Entry
	for _, csv := range csvs {
		if csv.IsCopied() {
			continue
		}

		if cachedSubscription, ok := csvSubscriptions[csv]; !ok || cachedSubscription == nil {
			// we might be in an incoherent state, so let's check with live clients to make sure
			realSubscriptions, err := s.listSubscriptions(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list subscriptions: %w", err)
			}
			for _, realSubscription := range realSubscriptions.Items {
				if realSubscription.Status.InstalledCSV == csv.Name {
					// oops, live cluster state is coherent
					return nil, fmt.Errorf("lister caches incoherent for CSV %s/%s - found owning Subscription %s/%s", csv.Namespace, csv.Name, realSubscription.Namespace, realSubscription.Name)
				}
			}
		}

		if failForwardEnabled {
			replacementChainEndsInFailure, err := isReplacementChainThatEndsInFailure(csv, ReplacementMapping(csvs))
			if err != nil {
				return nil, err
			}
			if csv.Status.Phase == v1alpha1.CSVPhaseReplacing && replacementChainEndsInFailure {
				continue
			}
		}

		entry, err := newEntryFromV1Alpha1CSV(csv)
		if err != nil {
			return nil, err
		}
		entry.SourceInfo = &cache.OperatorSourceInfo{
			Catalog:      s.key,
			Subscription: csvSubscriptions[csv],
		}

		entries = append(entries, entry)

		if anno, ok := csv.GetAnnotations()[projection.PropertiesAnnotationKey]; !ok {
			csvsMissingProperties = append(csvsMissingProperties, csv)
			if inferred, err := s.inferProperties(csv, subs); err != nil {
				s.logger.Printf("unable to infer properties for csv %q: %w", csv.Name, err)
			} else {
				entry.Properties = append(entry.Properties, inferred...)
			}
		} else if props, err := projection.PropertyListFromPropertiesAnnotation(anno); err != nil {
			return nil, fmt.Errorf("failed to retrieve properties of csv %q: %w", csv.GetName(), err)
		} else {
			entry.Properties = props
		}

		// Try to determine source package name from properties and add to SourceInfo.
		for _, p := range entry.Properties {
			if p.Type != opregistry.PackageType {
				continue
			}
			var pp opregistry.PackageProperty
			err := json.Unmarshal([]byte(p.Value), &pp)
			if err != nil {
				s.logger.Printf("failed to unmarshal package property of csv %q: %w", csv.Name, err)
				continue
			}
			entry.SourceInfo.Package = pp.PackageName
		}
	}

	if len(csvsMissingProperties) > 0 {
		names := make([]string, len(csvsMissingProperties))
		for i, csv := range csvsMissingProperties {
			names[i] = csv.GetName()
		}
		s.logger.Printf("considered csvs without properties annotation during resolution: %v", names)
	}

	return &cache.Snapshot{
		Entries: entries,
		Valid:   cache.ValidOnce(),
	}, nil
}

func (s *csvSource) inferProperties(csv *v1alpha1.ClusterServiceVersion, subs []*v1alpha1.Subscription) ([]*api.Property, error) {
	var properties []*api.Property

	packages := make(map[string]struct{})
	for _, sub := range subs {
		if sub.Status.InstalledCSV != csv.Name {
			continue
		}
		if pkg := sub.Spec.Package; pkg != "" {
			packages[pkg] = struct{}{}
		}
		// An erroneous package inference is possible if a user edits spec.package in a
		// Subscription that already references a ClusterServiceVersion via
		// status.installedCSV, but all recent versions of the catalog operator project
		// properties onto all ClusterServiceVersions they create.
	}
	if l := len(packages); l != 1 {
		s.logger.Printf("could not unambiguously infer package name for %q (found %d distinct package names)", csv.Name, l)
		return properties, nil
	}
	var pkg string
	for pkg = range packages {
		// Assign the single key to pkg.
	}
	var version string // Emit empty string rather than "0.0.0" if .spec.version is zero-valued.
	if !csv.Spec.Version.Version.Equals(semver.Version{}) {
		version = csv.Spec.Version.String()
	}
	pp, err := json.Marshal(opregistry.PackageProperty{
		PackageName: pkg,
		Version:     version,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal inferred package property: %w", err)
	}
	properties = append(properties, &api.Property{
		Type:  opregistry.PackageType,
		Value: string(pp),
	})

	return properties, nil
}

func newEntryFromV1Alpha1CSV(csv *v1alpha1.ClusterServiceVersion) (*cache.Entry, error) {
	providedAPIs := cache.EmptyAPISet()
	for _, crdDef := range csv.Spec.CustomResourceDefinitions.Owned {
		parts := strings.SplitN(crdDef.Name, ".", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("error parsing crd name: %s", crdDef.Name)
		}
		providedAPIs[opregistry.APIKey{Plural: parts[0], Group: parts[1], Version: crdDef.Version, Kind: crdDef.Kind}] = struct{}{}
	}
	for _, api := range csv.Spec.APIServiceDefinitions.Owned {
		providedAPIs[opregistry.APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}

	requiredAPIs := cache.EmptyAPISet()
	for _, crdDef := range csv.Spec.CustomResourceDefinitions.Required {
		parts := strings.SplitN(crdDef.Name, ".", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("error parsing crd name: %s", crdDef.Name)
		}
		requiredAPIs[opregistry.APIKey{Plural: parts[0], Group: parts[1], Version: crdDef.Version, Kind: crdDef.Kind}] = struct{}{}
	}
	for _, api := range csv.Spec.APIServiceDefinitions.Required {
		requiredAPIs[opregistry.APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}

	properties, err := providedAPIsToProperties(providedAPIs)
	if err != nil {
		return nil, err
	}
	dependencies, err := requiredAPIsToProperties(requiredAPIs)
	if err != nil {
		return nil, err
	}
	properties = append(properties, dependencies...)

	return &cache.Entry{
		Name:         csv.GetName(),
		Version:      &csv.Spec.Version.Version,
		ProvidedAPIs: providedAPIs,
		RequiredAPIs: requiredAPIs,
		SourceInfo:   &cache.OperatorSourceInfo{},
		Properties:   properties,
	}, nil
}
