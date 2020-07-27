package main

import (
	"context"
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	insecure              = "--insecure-policy=true"
	Skopeo                = "skopeo"
	debug                 = "--debug"
	skipTLS               = "--dest-tls-verify=false"
	skipCreds             = "--dest-no-creds=true"
	destCreds             = "--dest-creds="
	v2format              = "--format=v2s2"
	skopeoImage           = "quay.io/olmtest/skopeo:0.1.40"
)

func (r *Registry) skopeoCopy(client operatorclient.ClientInterface, newImage, newTag, oldImage, oldTag string) error {
	skopeoArgs := SkopeoCopyCmd(newImage, newTag, oldImage, "", r.auth)

	err := CreateSkopeoPod(client, skopeoArgs, r.namespace)
	if err != nil {
		return fmt.Errorf("error creating skopeo pod: %s", err)
	}

	// wait for skopeo pod to exit successfully
	awaitPod(client, r.namespace, Skopeo, func(pod *corev1.Pod) bool {
		return pod.Status.Phase == corev1.PodSucceeded
	})

	err = DeleteSkopeoPod(client, r.namespace)
	if err != nil {
		return fmt.Errorf("error deleting skopeo pod: %s", err)
	}
	return nil
}

func SkopeoCopyCmd(newImage, newTag, oldImage, oldTag, auth string) []string {
	newImageName := fmt.Sprint(newImage, newTag)
	oldImageName := fmt.Sprint(oldImage, oldTag)

	var creds string
	if auth == "" {
		creds = skipCreds
	} else {
		creds = fmt.Sprint(destCreds, auth)
	}

	cmd := []string{debug, insecure, "copy", skipTLS, v2format, creds, oldImageName, newImageName}

	return cmd
}

func CreateSkopeoPod(client operatorclient.ClientInterface, args []string, namespace string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      Skopeo,
			Namespace: namespace,
			Labels:    map[string]string{"name": Skopeo},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  Skopeo,
					Image: skopeoImage,
					Args:  args,
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			// ServiceAccountName: "builder",
		},
	}

	_, err := client.KubernetesInterface().CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func DeleteSkopeoPod(client operatorclient.ClientInterface, namespace string) error {
	err := client.KubernetesInterface().CoreV1().Pods(namespace).Delete(context.TODO(), Skopeo, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}
