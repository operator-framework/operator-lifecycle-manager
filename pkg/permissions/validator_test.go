package permissions

import (
	"reflect"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

func TestNewPermissionValidator(t *testing.T) {
	type args struct {
		lister operatorlister.OperatorLister
	}
	tests := []struct {
		name string
		args args
		want *PermissionValidator
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewPermissionValidator(tt.args.lister); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewPermissionValidator() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPermissionValidator_UserCanCreateV1Alpha1CSV(t *testing.T) {
	type fields struct {
		lister operatorlister.OperatorLister
	}
	type args struct {
		username string
		csv      *v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PermissionValidator{
				lister: tt.fields.lister,
			}
			if err := p.UserCanCreateV1Alpha1CSV(tt.args.username, tt.args.csv); (err != nil) != tt.wantErr {
				t.Errorf("UserCanCreateV1Alpha1CSV() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestToNamespaceRules(t *testing.T) {
	type args struct {
		namespace string
		perms     map[string]*resolver.OperatorPermissions
	}
	tests := []struct {
		name      string
		args      args
		wantRules []NamespaceRule
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotRules := ToNamespaceRules(tt.args.namespace, tt.args.perms); !reflect.DeepEqual(gotRules, tt.wantRules) {
				t.Errorf("ToNamespaceRules() = %v, want %v", gotRules, tt.wantRules)
			}
		})
	}
}

func TestWithoutOwnedAndRequired(t *testing.T) {
	type args struct {
		csv   *v1alpha1.ClusterServiceVersion
		rules []NamespaceRule
	}
	tests := []struct {
		name         string
		args         args
		wantFiltered []NamespaceRule
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if gotFiltered := WithoutOwnedAndRequired(tt.args.csv, tt.args.rules); !reflect.DeepEqual(gotFiltered, tt.wantFiltered) {
				t.Errorf("WithoutOwnedAndRequired() = %v, want %v", gotFiltered, tt.wantFiltered)
			}
		})
	}
}
