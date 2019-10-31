package permissions

import (
	"reflect"
	"testing"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewPermissionCreator(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	type args struct {
		client kubernetes.Interface
	}
	tests := []struct {
		name string
		args args
		want *PermissionCreator
	}{
		{
			name: "Client",
			args: args{client: fakeClient},
			want: &PermissionCreator{client: fakeClient},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewPermissionCreator(tt.args.client); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewPermissionCreator() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPermissionCreator_FromOperatorPermissions(t *testing.T) {
	type fields struct {
		client kubernetes.Interface
	}
	type args struct {
		namespace   string
		permissions map[string]*resolver.OperatorPermissions
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			fields: fields{
				client: fake.NewSimpleClientset(),
			},
			args: args{
				permissions: map[string]*resolver.OperatorPermissions{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PermissionCreator{
				client: tt.fields.client,
			}
			if err := p.FromOperatorPermissions(tt.args.namespace, tt.args.permissions); (err != nil) != tt.wantErr {
				t.Errorf("FromOperatorPermissions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
