package broker

import (
	"encoding/json"
	"fmt"

	osb "github.com/pmorie/go-open-service-broker-client/v2"
)

// parseKubernetesIdentity - creates a kubernetes identity from the
// orginating identity
func parseKubernetesIdentity(o osb.OriginatingIdentity) (*KubernetesUserInfo, error) {
	u := KubernetesUserInfo{}
	err := json.Unmarshal([]byte(o.Value), &u)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal json for value while parsing Kubernetes identity")
	}
	return &u, nil
}

// parseCloudFoundryIdentity - creates a cloud foundry identity from the
// orginating identity
func parseCloudFoundryIdentity(o osb.OriginatingIdentity) (*CloudFoundryUserInfo, error) {
	m := map[string]interface{}{}
	err := json.Unmarshal([]byte(o.Value), &m)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal json for value while parsing cloud foundry identity")
	}
	// Validate that user_id MUST be in the json object.
	var u interface{}
	var user string
	var ok bool
	if u, ok = m["user_id"]; !ok {
		return nil, fmt.Errorf("user_id key was not found in cloud foundry object")
	}
	user, ok = u.(string)
	if !ok {
		return nil, fmt.Errorf("user_id value was not a string in cloud foundry object")
	}
	delete(m, "user_id")
	c := CloudFoundryUserInfo{UserID: user, Extras: m}
	return &c, nil
}

// ParseIdentity - retrieve the identity union type
func ParseIdentity(o osb.OriginatingIdentity) (Identity, error) {
	identity := Identity{Platform: o.Platform}
	switch o.Platform {
	case osb.PlatformKubernetes:
		k, err := parseKubernetesIdentity(o)
		if err != nil {
			return identity, err
		}
		identity.Kubernetes = k
	case osb.PlatformCloudFoundry:
		c, err := parseCloudFoundryIdentity(o)
		if err != nil {
			return identity, err
		}
		identity.CloudFoundry = c
	default:
		m := map[string]interface{}{}
		err := json.Unmarshal([]byte(o.Value), &m)
		if err != nil {
			return identity, fmt.Errorf("unable to unmarshal json for value")
		}
		identity.Unknown = m
	}
	return identity, nil
}

// Identity - union type, used to access the correct originating identity
// implementation type
type Identity struct {
	Platform     string
	Kubernetes   *KubernetesUserInfo
	CloudFoundry *CloudFoundryUserInfo
	Unknown      map[string]interface{}
}

// KubernetesUserInfo - kubernetes user info object
type KubernetesUserInfo struct {
	Username string              `json:"username"`
	UID      string              `json:"uid"`
	Groups   []string            `json:"groups"`
	Extra    map[string][]string `json:"extra"`
}

// CloudFoundryUserInfo - cloud foundry user info object
type CloudFoundryUserInfo struct {
	UserID string
	Extras map[string]interface{}
}
