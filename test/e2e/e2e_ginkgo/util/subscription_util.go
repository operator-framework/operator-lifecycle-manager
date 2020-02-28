package util

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

var (
	Strategy = v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
			{
				Name: GenName("dep-"),
				Spec: newNginxDeployment(GenName("nginx-")),
			},
		},
	}
)
