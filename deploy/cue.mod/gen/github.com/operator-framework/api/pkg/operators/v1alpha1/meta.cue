package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    appsv1 "k8s.io/api/apps/v1"
    rbacv1 "k8s.io/api/rbac/v1"
)

#TypeMeta: metav1.#TypeMeta

#ObjectMeta: metav1.#ObjectMeta

#DeploymentSpec: appsv1.#DeploymentSpec

#PolicyRule: rbacv1.#PolicyRule