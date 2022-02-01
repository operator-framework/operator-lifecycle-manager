package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/pkg/util"
)

type Storage interface {
	Load(ctx context.Context, owner client.Object) ([]unstructured.Unstructured, error)
	Store(ctx context.Context, owner client.Object, objects []client.Object) error
}

var _ Storage = &ConfigMaps{}

type ConfigMaps struct {
	Client     client.Client
	Namespace  string
	NamePrefix string
}

func (s *ConfigMaps) Load(ctx context.Context, owner client.Object) ([]unstructured.Unstructured, error) {
	metadata, err := s.getMetadata(ctx, owner)
	if err != nil {
		return nil, err
	}
	configMaps := []corev1.ConfigMap{}
	for _, name := range metadata.Objects {
		key := types.NamespacedName{Namespace: s.Namespace, Name: name}
		cm := corev1.ConfigMap{}
		if err := s.Client.Get(ctx, key, &cm); err != nil {
			return nil, err
		}
		configMaps = append(configMaps, cm)
	}

	objects := []unstructured.Unstructured{}
	for _, cm := range configMaps {
		u := unstructured.Unstructured{}
		if err := convertConfigMapToObject(cm, &u); err != nil {
			return nil, err
		}
		objects = append(objects, u)
	}
	return objects, nil
}

type metadata struct {
	Objects []string `json:"objects"`
}

func (s *ConfigMaps) getMetadata(ctx context.Context, owner client.Object) (*metadata, error) {
	key := types.NamespacedName{Namespace: s.Namespace, Name: fmt.Sprintf("%smetadata-%s", s.NamePrefix, owner.GetName())}
	cm := corev1.ConfigMap{}
	if err := s.Client.Get(ctx, key, &cm); err != nil {
		return nil, err
	}
	m := metadata{}
	if err := json.Unmarshal([]byte(cm.Data["objects"]), &m.Objects); err != nil {
		return nil, err
	}
	return &m, nil
}

func convertConfigMapToObject(cm corev1.ConfigMap, obj client.Object) error {
	r, err := gzip.NewReader(bytes.NewReader(cm.BinaryData["object"]))
	if err != nil {
		return fmt.Errorf("create gzip reader for bundle object data: %v", err)
	}
	objData, err := ioutil.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read gzip data for bundle object: %v", err)
	}
	return yaml.Unmarshal(objData, obj)
}

func (s *ConfigMaps) Store(ctx context.Context, owner client.Object, objects []client.Object) error {
	actualConfigMaps, err := s.getExistingConfigMaps(ctx, owner)
	if err != nil {
		return err
	}

	desiredConfigMaps := []corev1.ConfigMap{}
	for _, obj := range objects {
		cm, err := s.buildObject(obj, owner)
		if err != nil {
			return err
		}
		desiredConfigMaps = append(desiredConfigMaps, *cm)
	}
	metadataCm, err := s.buildMetadata(desiredConfigMaps, owner)
	if err != nil {
		return err
	}
	desiredConfigMaps = append(desiredConfigMaps, *metadataCm)
	return s.createOrUpdateConfigMaps(ctx, actualConfigMaps, desiredConfigMaps)
}

func (s *ConfigMaps) getExistingConfigMaps(ctx context.Context, owner client.Object) ([]corev1.ConfigMap, error) {
	cmList := &corev1.ConfigMapList{}
	labels := map[string]string{
		"kuberpak.io/owner-name": owner.GetName(),
	}
	if err := s.Client.List(ctx, cmList, client.MatchingLabels(labels), client.InNamespace(s.Namespace)); err != nil {
		return nil, err
	}
	return cmList.Items, nil
}

func (s *ConfigMaps) buildObject(obj client.Object, owner client.Object) (*corev1.ConfigMap, error) {
	objData, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(objData))
	objCompressed := &bytes.Buffer{}
	gzipper := gzip.NewWriter(objCompressed)
	if _, err := gzipper.Write(objData); err != nil {
		return nil, fmt.Errorf("gzip object data: %v", err)
	}
	if err := gzipper.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %v", err)
	}
	gvk := obj.GetObjectKind().GroupVersionKind()

	labels := map[string]string{
		"kuberpak.io/owner-name":       owner.GetName(),
		"kuberpak.io/configmap-type":   "object",
		"kuberpak.io/object-group":     gvk.Group,
		"kuberpak.io/object-version":   gvk.Version,
		"kuberpak.io/object-kind":      gvk.Kind,
		"kuberpak.io/object-name":      obj.GetName(),
		"kuberpak.io/object-namespace": obj.GetNamespace(),
	}
	immutable := true
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%sobject-%s-%s", s.NamePrefix, owner.GetName(), hash[0:8]),
			Namespace: s.Namespace,
			Labels:    labels,
		},
		Immutable: &immutable,
		Data: map[string]string{
			"object-sha256": hash,
		},
		BinaryData: map[string][]byte{
			"object": objCompressed.Bytes(),
		},
	}
	if err := controllerutil.SetControllerReference(owner, cm, s.Client.Scheme()); err != nil {
		return nil, err
	}
	return cm, nil
}

func (s *ConfigMaps) buildMetadata(dcms []corev1.ConfigMap, owner client.Object) (*corev1.ConfigMap, error) {
	cmNames := []string{}
	for _, dcm := range dcms {
		cmNames = append(cmNames, dcm.Name)
	}
	sort.Strings(cmNames)
	objectJSON, err := json.Marshal(cmNames)
	if err != nil {
		return nil, err
	}

	immutable := true
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: s.Namespace,
			Name:      fmt.Sprintf("%smetadata-%s", s.NamePrefix, owner.GetName()),
			Labels: map[string]string{
				"kuberpak.io/owner-name":     owner.GetName(),
				"kuberpak.io/configmap-type": "metadata",
			},
		},
		Immutable: &immutable,
		Data: map[string]string{
			"objects": string(objectJSON),
		},
	}
	if err := controllerutil.SetControllerReference(owner, &cm, s.Client.Scheme()); err != nil {
		return nil, err
	}
	return &cm, nil
}

func (s *ConfigMaps) createOrUpdateConfigMaps(ctx context.Context, acms, dcms []corev1.ConfigMap) error {
	acmMap := map[types.NamespacedName]corev1.ConfigMap{}
	for _, acm := range acms {
		acmMap[types.NamespacedName{Namespace: acm.Namespace, Name: acm.Name}] = acm
	}
	for _, dcm := range dcms {
		dcm := dcm
		key := types.NamespacedName{Namespace: dcm.Namespace, Name: dcm.Name}
		acm, ok := acmMap[key]
		if ok {
			if util.ConfigMapsEqual(acm, dcm) {
				delete(acmMap, key)
				continue
			}
		}
		if err := s.Client.Get(ctx, client.ObjectKeyFromObject(&dcm), &acm); err == nil {
			if err := s.Client.Delete(ctx, &acm); err != nil {
				return err
			}
		}
		if err := s.Client.Create(ctx, &dcm); err != nil {
			return err
		}
	}
	for _, acm := range acmMap {
		acm := acm
		if err := s.Client.Delete(ctx, &acm); err != nil {
			return err
		}
	}
	return nil
}

//func (s *ConfigMaps) Get(ctx context.Context, key types.NamespacedName, obj client.Object) error {
//	obj.SetName(key.Name)
//	obj.SetNamespace(key.Namespace)
//
//	cm, err := s.getConfigMapFor(ctx, obj)
//	if err != nil {
//		return err
//	}
//	return convertConfigMapToObject(cm, obj)
//}

//
//func (s *ConfigMaps) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
//	cm, err := s.buildObject(obj)
//	if err != nil {
//		return err
//	}
//	return s.Client.Create(ctx, cm, opts...)
//}
//
//func (s *ConfigMaps) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
//	cm, err := s.getConfigMapFor(ctx, obj)
//	if err != nil {
//		return err
//	}
//	return s.Client.Delete(ctx, cm, opts...)
//}
//
//func (s *ConfigMaps) getConfigMapFor(ctx context.Context, obj client.Object) (*corev1.ConfigMap, error) {
//	gvk, err := apiutil.GVKForObject(obj, s.Client.Scheme())
//	if err != nil {
//		return nil, err
//	}
//	obj.GetObjectKind().SetGroupVersionKind(gvk)
//
//	configMapList := &corev1.ConfigMapList{}
//	if err := s.Client.List(ctx, configMapList, client.InNamespace(s.Namespace), client.MatchingLabels(labelsFor(obj))); err != nil {
//		return nil, err
//	}
//	switch len(configMapList.Items) {
//	case 0:
//		rm, err := s.RESTMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
//		if err != nil {
//			panic(fmt.Sprintf("rest mapping failed: %v", err))
//		}
//		return nil, apierrors.NewNotFound(rm.Resource.GroupResource(), key.Name)
//	case 1:
//		return &configMapList.Items[1], nil
//	default:
//		duplicates := []string{}
//		for _, i := range configMapList.Items {
//			duplicates = append(duplicates, i.Name)
//		}
//		return nil, fmt.Errorf("duplicate objects found: %v", duplicates)
//	}
//}
//
//func labelsFor(obj client.Object) map[string]string {
//	gvk := obj.GetObjectKind().GroupVersionKind()
//	labels := map[string]string{
//		"kuberpak.io/object-group":   gvk.Group,
//		"kuberpak.io/object-version": gvk.Version,
//		"kuberpak.io/object-kind":    gvk.Kind,
//		"kuberpak.io/object-name":    obj.GetName(),
//	}
//	if obj.GetNamespace() != "" {
//		labels["kuberpak.io/object-namespace"] = obj.GetNamespace()
//	}
//	return labels
//}
