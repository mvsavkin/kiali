package kubernetes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v2"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/log"
)

var (
	portNameMatcher    = regexp.MustCompile(`^[\-].*`)
	portProtocols      = [...]string{"grpc", "http", "http2", "https", "mongo", "redis", "tcp", "tls", "udp", "mysql"}
	istioConfigmapName = "istio"
)

// Aux method to fetch proper (RESTClient, APIVersion) per API group
func (in *K8SClient) getApiClientVersion(apiGroup string) (*rest.RESTClient, string) {
	if apiGroup == ConfigGroupVersion.Group {
		return in.istioConfigApi, ApiConfigVersion
	} else if apiGroup == NetworkingGroupVersion.Group {
		return in.istioNetworkingApi, ApiNetworkingVersion
	} else if apiGroup == AuthenticationGroupVersion.Group {
		return in.istioAuthenticationApi, ApiAuthenticationVersion
	} else if apiGroup == RbacGroupVersion.Group {
		return in.istioRbacApi, ApiRbacVersion
	} else if apiGroup == MaistraAuthenticationGroupVersion.Group {
		return in.maistraAuthenticationApi, ApiMaistraAuthenticationVersion
	} else if apiGroup == MaistraRbacGroupVersion.Group {
		return in.maistraRbacApi, ApiMaistraRbacVersion
	} else if apiGroup == SecurityGroupVersion.Group {
		return in.istioSecurityApi, ApiSecurityVersion
	}
	return nil, ""
}

// CreateIstioObject creates an Istio object
func (in *K8SClient) CreateIstioObject(api, namespace, resourceType, json string) (IstioObject, error) {
	var result runtime.Object
	var err error

	typeMeta := meta_v1.TypeMeta{
		Kind:       "",
		APIVersion: "",
	}
	typeMeta.Kind = PluralType[resourceType]
	byteJson := []byte(json)

	var apiClient *rest.RESTClient
	apiClient, typeMeta.APIVersion = in.getApiClientVersion(api)
	if apiClient == nil {
		return nil, fmt.Errorf("%s is not supported in CreateIstioObject operation", api)
	}

	// MeshPeerAuthentications and ClusterRbacConfigs are cluster scope objects
	// Update: Removed the namespace filter as it doesn't work well in all platforms
	// https://issues.jboss.org/browse/KIALI-3223
	if resourceType == MeshPolicies || resourceType == ClusterRbacConfigs {
		result, err = apiClient.Post().Resource(resourceType).Body(byteJson).Do().Get()
	} else {
		result, err = apiClient.Post().Namespace(namespace).Resource(resourceType).Body(byteJson).Do().Get()
	}
	if err != nil {
		return nil, err
	}

	istioObject, ok := result.(*GenericIstioObject)
	if !ok {
		return nil, fmt.Errorf("%s/%s doesn't return an IstioObject object", namespace, resourceType)
	}
	istioObject.SetTypeMeta(typeMeta)
	return istioObject, err
}

// DeleteIstioObject deletes an Istio object from either config api or networking api
func (in *K8SClient) DeleteIstioObject(api, namespace, resourceType, name string) error {
	log.Debugf("DeleteIstioObject input: %s / %s / %s / %s", api, namespace, resourceType, name)
	var err error
	apiClient, _ := in.getApiClientVersion(api)
	if apiClient == nil {
		return fmt.Errorf("%s is not supported in DeleteIstioObject operation", api)
	}
	// MeshPeerAuthentications and ClusterRbacConfigs are cluster scope objects
	// Update: Removed the namespace filter as it doesn't work well in all platforms
	// https://issues.jboss.org/browse/KIALI-3223
	if resourceType == MeshPolicies || resourceType == ClusterRbacConfigs {
		_, err = apiClient.Delete().Resource(resourceType).Name(name).Do().Get()
	} else {
		_, err = apiClient.Delete().Namespace(namespace).Resource(resourceType).Name(name).Do().Get()
	}
	return err
}

// UpdateIstioObject updates an Istio object from either config api or networking api
func (in *K8SClient) UpdateIstioObject(api, namespace, resourceType, name, jsonPatch string) (IstioObject, error) {
	log.Debugf("UpdateIstioObject input: %s / %s / %s / %s", api, namespace, resourceType, name)
	var result runtime.Object
	var err error

	typeMeta := meta_v1.TypeMeta{
		Kind:       "",
		APIVersion: "",
	}
	typeMeta.Kind = PluralType[resourceType]
	bytePatch := []byte(jsonPatch)
	var apiClient *rest.RESTClient
	apiClient, typeMeta.APIVersion = in.getApiClientVersion(api)
	if apiClient == nil {
		return nil, fmt.Errorf("%s is not supported in UpdateIstioObject operation", api)
	}
	// MeshPeerAuthentications and ClusterRbacConfigs are cluster scope objects
	// Update: Removed the namespace filter as it doesn't work well in all platforms
	// https://issues.jboss.org/browse/KIALI-3223
	if resourceType == MeshPolicies || resourceType == ClusterRbacConfigs {
		result, err = apiClient.Patch(types.MergePatchType).Resource(resourceType).SubResource(name).Body(bytePatch).Do().Get()
	} else {
		result, err = apiClient.Patch(types.MergePatchType).Namespace(namespace).Resource(resourceType).SubResource(name).Body(bytePatch).Do().Get()
	}
	if err != nil {
		return nil, err
	}
	istioObject, ok := result.(*GenericIstioObject)
	if !ok {
		return nil, fmt.Errorf("%s/%s doesn't return an IstioObject object", namespace, name)
	}
	istioObject.SetTypeMeta(typeMeta)
	return istioObject, err
}

func (in *K8SClient) GetIstioObjects(namespace, resourceType, labelSelector string) ([]IstioObject, error) {
	var apiClient *rest.RESTClient
	var apiGroup, apiVersion string
	var ok bool
	if apiGroup, ok = ResourceTypesToAPI[resourceType]; ok {
		apiClient, apiVersion = in.getApiClientVersion(apiGroup)
	} else {
		return []IstioObject{}, fmt.Errorf("%s not found in ResourcesTypeToAPI", resourceType)
	}

	if apiGroup == NetworkingGroupVersion.Group && !in.hasNetworkingResource(resourceType) {
		return []IstioObject{}, nil
	}

	if apiGroup == ConfigGroupVersion.Group && !in.hasConfigResource(resourceType) {
		return []IstioObject{}, nil
	}

	if apiGroup == AuthenticationGroupVersion.Group && !in.hasAuthenticationResource(resourceType) {
		return []IstioObject{}, nil
	}

	if apiGroup == RbacGroupVersion.Group && !in.hasRbacResource(resourceType) {
		return []IstioObject{}, nil
	}

	if apiGroup == SecurityGroupVersion.Group && !in.hasSecurityResource(resourceType) {
		return []IstioObject{}, nil
	}

	var result runtime.Object
	var err error
	if resourceType == MeshPolicies || resourceType == ClusterRbacConfigs {
		result, err = apiClient.Get().Resource(resourceType).Param("labelSelector", labelSelector).Do().Get()
	} else {
		result, err = apiClient.Get().Namespace(namespace).Resource(resourceType).Param("labelSelector", labelSelector).Do().Get()
	}
	if err != nil {
		return nil, err
	}
	istioList, ok := result.(*GenericIstioObjectList)
	if !ok {
		return nil, fmt.Errorf("%s/%s doesn't return a list", namespace, resourceType)
	}
	typeMeta := meta_v1.TypeMeta{
		Kind:       PluralType[resourceType],
		APIVersion: apiVersion,
	}
	list := make([]IstioObject, 0)
	for _, item := range istioList.GetItems() {
		i := item.DeepCopyIstioObject()
		i.SetTypeMeta(typeMeta)
		list = append(list, i)
	}
	return list, nil
}

func (in *K8SClient) GetIstioObject(namespace, resourceType, name string) (IstioObject, error) {
	var apiClient *rest.RESTClient
	var apiGroup, apiVersion string
	var ok bool
	if apiGroup, ok = ResourceTypesToAPI[resourceType]; ok {
		apiClient, apiVersion = in.getApiClientVersion(apiGroup)
	} else {
		return nil, fmt.Errorf("%s not found in ResourcesTypeToAPI", resourceType)
	}

	var result runtime.Object
	var err error
	if resourceType == MeshPolicies || resourceType == ClusterRbacConfigs {
		result, err = apiClient.Get().Resource(resourceType).SubResource(name).Do().Get()
	} else {
		result, err = apiClient.Get().Namespace(namespace).Resource(resourceType).SubResource(name).Do().Get()
	}
	if err != nil {
		return nil, err
	}
	typeMeta := meta_v1.TypeMeta{
		Kind:       PluralType[resourceType],
		APIVersion: apiVersion,
	}
	istioObject, ok := result.(*GenericIstioObject)
	if !ok {
		return nil, fmt.Errorf("%s/%s/%s doesn't return an Istio object", namespace, resourceType, name)
	}
	io := istioObject.DeepCopyIstioObject()
	io.SetTypeMeta(typeMeta)
	return io, nil
}

func (in *K8SClient) hasNetworkingResource(resource string) bool {
	return in.getNetworkingResources()[resource]
}

func (in *K8SClient) getNetworkingResources() map[string]bool {
	if in.networkingResources != nil {
		return *in.networkingResources
	}

	networkingResources := map[string]bool{}
	path := fmt.Sprintf("/apis/%s", ApiNetworkingVersion)
	resourceListRaw, err := in.k8s.RESTClient().Get().AbsPath(path).Do().Raw()
	if err == nil {
		resourceList := meta_v1.APIResourceList{}
		if errMarshall := json.Unmarshal(resourceListRaw, &resourceList); errMarshall == nil {
			for _, resource := range resourceList.APIResources {
				networkingResources[resource.Name] = true
			}
		}
	}
	in.networkingResources = &networkingResources

	return *in.networkingResources
}

func (in *K8SClient) hasConfigResource(resource string) bool {
	return in.getConfigResources()[resource]
}

func (in *K8SClient) getConfigResources() map[string]bool {
	if in.configResources != nil {
		return *in.configResources
	}

	configResources := map[string]bool{}
	path := fmt.Sprintf("/apis/%s", ApiConfigVersion)
	resourceListRaw, err := in.k8s.RESTClient().Get().AbsPath(path).Do().Raw()
	if err == nil {
		resourceList := meta_v1.APIResourceList{}
		if errMarshall := json.Unmarshal(resourceListRaw, &resourceList); errMarshall == nil {
			for _, resource := range resourceList.APIResources {
				configResources[resource.Name] = true
			}
		}
	}
	in.configResources = &configResources

	return *in.configResources
}

func (in *K8SClient) hasRbacResource(resource string) bool {
	return in.getRbacResources()[resource]
}

func (in *K8SClient) getRbacResources() map[string]bool {
	if in.rbacResources != nil {
		return *in.rbacResources
	}

	rbacResources := map[string]bool{}
	path := fmt.Sprintf("/apis/%s", ApiRbacVersion)
	resourceListRaw, err := in.k8s.RESTClient().Get().AbsPath(path).Do().Raw()
	if err == nil {
		resourceList := meta_v1.APIResourceList{}
		if errMarshall := json.Unmarshal(resourceListRaw, &resourceList); errMarshall == nil {
			for _, resource := range resourceList.APIResources {
				rbacResources[resource.Name] = true
			}
		}
	}
	in.rbacResources = &rbacResources

	return *in.rbacResources
}

func (in *K8SClient) hasSecurityResource(resource string) bool {
	return in.getSecurityResources()[resource]
}

func (in *K8SClient) getSecurityResources() map[string]bool {
	if in.securityResources != nil {
		return *in.securityResources
	}

	securityResources := map[string]bool{}
	path := fmt.Sprintf("/apis/%s", ApiSecurityVersion)
	resourceListRaw, err := in.k8s.RESTClient().Get().AbsPath(path).Do().Raw()
	if err == nil {
		resourceList := meta_v1.APIResourceList{}
		if errMarshall := json.Unmarshal(resourceListRaw, &resourceList); errMarshall == nil {
			for _, resource := range resourceList.APIResources {
				securityResources[resource.Name] = true
			}
		}
	}
	in.securityResources = &securityResources

	return *in.securityResources
}

func (in *K8SClient) hasAuthenticationResource(resource string) bool {
	return in.getAuthenticationResources()[resource]
}

func (in *K8SClient) getAuthenticationResources() map[string]bool {
	if in.authenticationResources != nil {
		return *in.authenticationResources
	}

	authenticationResources := map[string]bool{}
	path := fmt.Sprintf("/apis/%s", ApiAuthenticationVersion)
	resourceListRaw, err := in.k8s.RESTClient().Get().AbsPath(path).Do().Raw()
	if err == nil {
		resourceList := meta_v1.APIResourceList{}
		if errMarshall := json.Unmarshal(resourceListRaw, &resourceList); errMarshall == nil {
			for _, resource := range resourceList.APIResources {
				authenticationResources[resource.Name] = true
			}
		}
	}
	in.authenticationResources = &authenticationResources

	return *in.authenticationResources
}

// GetAuthorizationDetails returns ServiceRoles, ServiceRoleBindings and ClusterRbacDetails
func (in *K8SClient) GetAuthorizationDetails(namespace string) (*RBACDetails, error) {
	rb := &RBACDetails{}

	errChan := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(4)

	go func(errChan chan error) {
		defer wg.Done()
		if aps, err := in.GetIstioObjects(namespace, AuthorizationPolicies, ""); err == nil {
			rb.AuthorizationPolicies = aps
		} else {
			errChan <- err
		}
	}(errChan)

	go func(errChan chan error) {
		defer wg.Done()
		if srb, err := in.GetIstioObjects(namespace, ServiceRoleBindings, ""); err == nil {
			rb.ServiceRoleBindings = srb
		} else {
			errChan <- err
		}
	}(errChan)

	go func(errChan chan error) {
		defer wg.Done()
		if sr, err := in.GetIstioObjects(namespace, ServiceRoles, ""); err == nil {
			rb.ServiceRoles = sr
		} else {
			errChan <- err
		}
	}(errChan)

	go func(errChan chan error) {
		defer wg.Done()
		// Maistra has migrated ClusterRbacConfigs to ServiceMeshRbacConfigs resources
		if !in.IsMaistraApi() {
			if crc, err := in.GetIstioObjects("", ClusterRbacConfigs, ""); err == nil {
				rb.ClusterRbacConfigs = crc
			} else {
				errChan <- err
			}
		} else {
			if smrc, err := in.GetIstioObjects(namespace, ServiceMeshRbacConfigs, ""); err == nil {
				rb.ServiceMeshRbacConfigs = smrc
			} else {
				errChan <- err
			}
		}
	}(errChan)

	wg.Wait()
	close(errChan)

	for e := range errChan {
		if e != nil { // Check that default value wasn't returned
			return rb, e
		}
	}

	return rb, nil
}

func (in *K8SClient) GetIstioConfigMap() (*IstioMeshConfig, error) {
	meshConfig := &IstioMeshConfig{}

	cfg := config.Get()
	istioConfig, err := in.GetConfigMap(cfg.IstioNamespace, istioConfigmapName)
	if err != nil {
		log.Warningf("GetIstioConfigMap: Cannot retrieve Istio ConfigMap.")
		return nil, err
	}

	meshConfigYaml, ok := istioConfig.Data["mesh"]
	log.Tracef("meshConfig: %v", meshConfigYaml)
	if !ok {
		log.Warningf("GetIstioConfigMap: Cannot find Istio mesh configuration.")
		return nil, err
	}

	err = yaml.Unmarshal([]byte(meshConfigYaml), &meshConfig)
	if err != nil {
		log.Warningf("GetIstioConfigMap: Cannot read Istio mesh configuration.")
		return nil, err
	}

	return meshConfig, nil
}

func (in *K8SClient) IsMixerDisabled() bool {
	if in.isMixerDisabled != nil {
		return *in.isMixerDisabled
	}

	meshConfig, err := in.GetIstioConfigMap()
	if err != nil {
		log.Warningf("IsMixerDisabled: Cannot read Istio mesh configuration.")
		return true
	}

	log.Debugf("IsMixerDisabled: %t", meshConfig.DisableMixerHttpReports)

	// References:
	//   * https://github.com/istio/api/pull/1112
	//   * https://github.com/istio/istio/pull/17695
	//   * https://github.com/istio/istio/issues/15935
	in.isMixerDisabled = &meshConfig.DisableMixerHttpReports
	return *in.isMixerDisabled
}

// ServiceEntryHostnames returns a list of hostnames defined in the ServiceEntries Specs. Key in the resulting map is the protocol (in lowercase) + hostname
// exported for test
func ServiceEntryHostnames(serviceEntries []IstioObject) map[string][]string {
	hostnames := make(map[string][]string)

	for _, v := range serviceEntries {
		if hostsSpec, found := v.GetSpec()["hosts"]; found {
			if hosts, ok := hostsSpec.([]interface{}); ok {
				// Seek the protocol
				for _, h := range hosts {
					if hostname, ok := h.(string); ok {
						hostnames[hostname] = make([]string, 0, 1)
					}
				}
			}
		}
		if portsSpec, found := v.GetSpec()["ports"]; found {
			if portsArray, ok := portsSpec.([]interface{}); ok {
				for _, portDef := range portsArray {
					if ports, ok := portDef.(map[string]interface{}); ok {
						if proto, found := ports["protocol"]; found {
							if protocol, ok := proto.(string); ok {
								protocol = mapPortToVirtualServiceProtocol(protocol)
								for host := range hostnames {
									hostnames[host] = append(hostnames[host], protocol)
								}
							}
						}
					}
				}
			}
		}
	}

	return hostnames
}

// mapPortToVirtualServiceProtocol transforms Istio's Port-definitions' protocol names to VirtualService's protocol names
func mapPortToVirtualServiceProtocol(proto string) string {
	// http: HTTP/HTTP2/GRPC/ TLS-terminated-HTTPS and service entry ports using HTTP/HTTP2/GRPC protocol
	// tls: HTTPS/TLS protocols (i.e. with “passthrough” TLS mode) and service entry ports using HTTPS/TLS protocols.
	// tcp: everything else

	switch proto {
	case "HTTP":
		fallthrough
	case "HTTP2":
		fallthrough
	case "GRPC":
		return "http"
	case "HTTPS":
		fallthrough
	case "TLS":
		return "tls"
	default:
		return "tcp"
	}
}

// ValidaPort parses the Istio Port definition and validates the naming scheme
func ValidatePort(portDef interface{}) bool {
	return MatchPortNameRule(parsePort(portDef))
}

func parsePort(portDef interface{}) (string, string) {
	var name, proto string
	if port, ok := portDef.(map[string]interface{}); ok {
		if portNameDef, found := port["name"]; found {
			if portName, ok := portNameDef.(string); ok {
				name = portName
			}
		}
		if protocolDef, found := port["protocol"]; found {
			if protocol, ok := protocolDef.(string); ok {
				proto = protocol
			}
		}
	}

	return name, proto
}

func MatchPortNameRule(portName, protocol string) bool {
	protocol = strings.ToLower(protocol)
	// Check that portName begins with the protocol

	if protocol == "tcp" || protocol == "udp" {
		// TCP and UDP protocols do not care about the name
		return true
	}

	if !strings.HasPrefix(portName, protocol) {
		return false
	}

	// If longer than protocol, then it must adhere to <protocol>[-suffix]
	// and if there's -, then there must be a suffix ..
	if len(portName) > len(protocol) {
		restPortName := portName[len(protocol):]
		return portNameMatcher.MatchString(restPortName)
	}

	// Case portName == protocolName
	return true
}

func MatchPortNameWithValidProtocols(portName string) bool {
	for _, protocol := range portProtocols {
		if strings.HasPrefix(portName, protocol) &&
			(strings.ToLower(portName) == protocol || portNameMatcher.MatchString(portName[len(protocol):])) {
			return true
		}
	}
	return false
}

// GatewayNames extracts the gateway names for easier matching
func GatewayNames(gateways [][]IstioObject) map[string]struct{} {
	var empty struct{}
	names := make(map[string]struct{})
	for _, ns := range gateways {
		for _, gw := range ns {
			gw := gw
			clusterName := gw.GetObjectMeta().ClusterName
			if clusterName == "" {
				clusterName = config.Get().ExternalServices.Istio.IstioIdentityDomain
			}
			names[ParseHost(gw.GetObjectMeta().Name, gw.GetObjectMeta().Namespace, clusterName).String()] = empty
		}
	}
	return names
}

func PeerAuthnHasStrictMTLS(peerAuthn IstioObject) bool {
	_, mode := PeerAuthnHasMTLSEnabled(peerAuthn)
	return mode == "STRICT"
}

func PeerAuthnHasMTLSEnabled(peerAuthn IstioObject) (bool, string) {
	// It is no globally enabled when has targets
	if peerAuthn.HasMatchLabelsSelector() {
		return false, ""
	}

	// It is globally enabled when mtls is in STRICT mode
	if mtls, mtlsPresent := peerAuthn.GetSpec()["mtls"]; mtlsPresent {
		if mtlsMap, ok := mtls.(map[string]interface{}); ok {
			if modeItf, found := mtlsMap["mode"]; found {
				if mode, ok := modeItf.(string); ok {
					return mode == "STRICT" || mode == "PERMISSIVE", mode
				} else {
					return false, ""
				}
			} else {
				return true, "PERMISSIVE"
			}
		}
	}

	return false, ""
}

func DestinationRuleHasMeshWideMTLSEnabled(destinationRule IstioObject) (bool, string) {
	// Following the suggested procedure to enable mesh-wide mTLS, host might be '*.local':
	// https://istio.io/docs/tasks/security/authn-policy/#globally-enabling-istio-mutual-tls
	return DestinationRuleHasMTLSEnabledForHost("*.local", destinationRule)
}

func DestinationRuleHasNamespaceWideMTLSEnabled(namespace string, destinationRule IstioObject) (bool, string) {
	// Following the suggested procedure to enable namespace-wide mTLS, host might be '*.namespace.svc.cluster.local'
	// https://istio.io/docs/tasks/security/authn-policy/#namespace-wide-policy
	nsHost := fmt.Sprintf("*.%s.%s", namespace, config.Get().ExternalServices.Istio.IstioIdentityDomain)
	return DestinationRuleHasMTLSEnabledForHost(nsHost, destinationRule)
}

func DestinationRuleHasMTLSEnabledForHost(expectedHost string, destinationRule IstioObject) (bool, string) {
	host, hostPresent := destinationRule.GetSpec()["host"]
	if !hostPresent || host != expectedHost {
		return false, ""
	}

	if trafficPolicy, trafficPresent := destinationRule.GetSpec()["trafficPolicy"]; trafficPresent {
		if trafficCasted, ok := trafficPolicy.(map[string]interface{}); ok {
			if tls, found := trafficCasted["tls"]; found {
				if tlsCasted, ok := tls.(map[string]interface{}); ok {
					if mode, found := tlsCasted["mode"]; found {
						if modeCasted, ok := mode.(string); ok {
							return modeCasted == "ISTIO_MUTUAL", modeCasted
						}
					}
				}
			}
		}
	}

	return false, ""
}
