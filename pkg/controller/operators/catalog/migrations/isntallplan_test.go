package migrations

import (
    "github.com/operator-framework/api/pkg/operators/v1alpha1"
    "github.com/stretchr/testify/require"
    "testing"
)

func TestMigrateInstallPlan_MatchInstallPlan(t *testing.T) {
    for _, tc := range []struct {
        description string
        approval    v1alpha1.Approval
        approved    bool
        step        *v1alpha1.Step
        expectMatch bool
    }{
        {
            description: "Automatic approval: ignore",
            approval:    v1alpha1.ApprovalAutomatic,
            expectMatch: false,
        }, {
            description: "Manual approval and is approved: ignore",
            approval:    v1alpha1.ApprovalManual,
            approved:    true,
            expectMatch: false,
        }, {
            description: "Manual approval, not approved, has 4.14 ClusterRole step: match",
            approval:    v1alpha1.ApprovalManual,
            approved:    false,
            step: &v1alpha1.Step{
                Resolving: "some-operator.v1.0.0",
                Resource: v1alpha1.StepResource{
                    Group: "rbac.authorization.k8s.io",
                    Kind:  "ClusterRole",
                    Name:  "some-operator.v1.0.0-something-random-d34db33fff",
                },
            },
            expectMatch: true,
        }, {
            description: "Manual approval, not approved, has 4.14 ClusterRoleBinding step: match",
            approval:    v1alpha1.ApprovalManual,
            approved:    false,
            step: &v1alpha1.Step{
                Resolving: "some-operator.v1.0.0",
                Resource: v1alpha1.StepResource{
                    Group: "rbac.authorization.k8s.io",
                    Kind:  "ClusterRoleBinding",
                    Name:  "some-operator.v1.0.0-something-random-d34db33fff",
                },
            },
            expectMatch: true,
        }, {
            description: "Manual approval, not approved, has 4.14 Role step: match",
            approval:    v1alpha1.ApprovalManual,
            approved:    false,
            step: &v1alpha1.Step{
                Resolving: "some-operator.v1.0.0",
                Resource: v1alpha1.StepResource{
                    Group: "rbac.authorization.k8s.io",
                    Kind:  "Role",
                    Name:  "some-operator.v1.0.0-something-random-d34db33fff",
                },
            },
            expectMatch: true,
        }, {
            description: "Manual approval, not approved, has 4.14 RoleBinding step: match",
            approval:    v1alpha1.ApprovalManual,
            approved:    false,
            step: &v1alpha1.Step{
                Resolving: "some-operator.v1.0.0",
                Resource: v1alpha1.StepResource{
                    Group: "rbac.authorization.k8s.io",
                    Kind:  "RoleBinding",
                    Name:  "some-operator.v1.0.0-something-random-d34db33fff",
                },
            },
            expectMatch: true,
        }, {
            description: "Manual approval, not approved, has rbac step without 4.14 naming format (bad suffix): ignore",
            approval:    v1alpha1.ApprovalManual,
            approved:    false,
            step: &v1alpha1.Step{
                Resolving: "some-operator.v1.0.0",
                Resource: v1alpha1.StepResource{
                    Group: "rbac.authorization.k8s.io",
                    Kind:  "RoleBinding",
                    Name:  "some-operator.v1.0.0-something-random-ABCDEDF",
                },
            },
            expectMatch: false,
        }, {
            description: "Manual approval, not approved, has rbac step without 4.14 naming format (bad prefix): ignore",
            approval:    v1alpha1.ApprovalManual,
            approved:    false,
            step: &v1alpha1.Step{
                Resolving: "some-operator.v1.0.0",
                Resource: v1alpha1.StepResource{
                    Group: "rbac.authorization.k8s.io",
                    Kind:  "Role",
                    Name:  "some-operator-something-random-d34db33fff",
                },
            },
            expectMatch: false,
        }, {
            description: "Manual approval, not approved, has rbac step without 4.14 naming format (bad suffix/prefix): ignore",
            approval:    v1alpha1.ApprovalManual,
            approved:    false,
            step: &v1alpha1.Step{
                Resolving: "some-operator.v1.0.0",
                Resource: v1alpha1.StepResource{
                    Group: "rbac.authorization.k8s.io",
                    Kind:  "Role",
                    Name:  "some-operator-something",
                },
            },
            expectMatch: false,
        },
    } {
        t.Run(tc.description, func(t *testing.T) {
            ip := &v1alpha1.InstallPlan{
                Spec: v1alpha1.InstallPlanSpec{
                    Approval: tc.approval,
                    Approved: tc.approved,
                },
                Status: v1alpha1.InstallPlanStatus{
                    Plan: []*v1alpha1.Step{tc.step},
                },
            }
            require.Equal(t, tc.expectMatch, IsOldStyleInstallPlan(ip))
        })
    }
}
