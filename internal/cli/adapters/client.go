package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SAP/crossplane-provider-cloudfoundry/apis/resources/v1alpha1"
	"github.com/SAP/crossplane-provider-cloudfoundry/apis/v1beta1"
	"github.com/SAP/crossplane-provider-cloudfoundry/internal/cli/pkg/utils"
	"github.com/SAP/crossplane-provider-cloudfoundry/internal/crossplaneimport/client"
	"github.com/SAP/crossplane-provider-cloudfoundry/internal/crossplaneimport/kubernetes"
	cfv3 "github.com/cloudfoundry/go-cfclient/v3/client"
	cfconfig "github.com/cloudfoundry/go-cfclient/v3/config"
	"gopkg.in/alecthomas/kingpin.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

var(
	errIsSSHEnabled = "Could not check if SSH is enabled for the space"
	errListOrganizations = "Could not list organizations"
	errCreateCFConfig = "Could not create CF config"
	errCreateK8sClient = "Could not create Kubernetes client"
	errGetProviderConfig = "Could not get provider config"
	errGetSecret = "Could not get secret"
	errExtractCredentials = "Credentials key not found in secret data"
	errExtractApiEndpoint = "API endpoint key not found in secret data"
	errUnmarshalCredentials = "Failed to unmarshal credentials JSON"
	errGetOrgReference = "Could not get data about referenced organization"
	errGetSpaceReference = "Could not get data about referenced space"
)

// CFCredentials implements the Credentials interface
type CFCredentials struct {
	ApiEndpoint		string;
	Email 			string;
	Password 		string;
}

func (c *CFCredentials) GetAuthData() map[string][]byte {
	return map[string][]byte{
		"apiEndpoint": []byte(c.ApiEndpoint),
		"email":       []byte(c.Email),
		"password":    []byte(c.Password),
	}
}

// CFClient implements the ProviderClient interface
type CFClient struct {
	client cfv3.Client
}

func (c *CFClient) GetResourcesByType(ctx context.Context, resourceType string, filter map[string]string) ([]interface{}, error) {
	switch resourceType {
	case v1alpha1.Space_Kind:
		return c.getSpaces(ctx, filter)
	case v1alpha1.Org_Kind:
		return c.getOrganizations(ctx, filter)
	case v1alpha1.App_Kind:
		return c.getApps(ctx, filter)
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}
}

func (c *CFClient) getSpaces(ctx context.Context, filter map[string]string) ([]interface{}, error) {
	// Get name filter
	name, ok := filter["name"]
	if !ok {
		return nil, fmt.Errorf("name filter is required for spaces")
	}
	orgName, ok := filter["org"]
	if !ok {
		return nil, fmt.Errorf("org-reference filter is required for spaces")
	}

	// get referenced org
	orgRefFilter := cfv3.OrganizationListOptions{Names: cfv3.Filter{Values: []string{orgName}}}
	orgRef, err := c.client.Organizations.ListAll(ctx, &orgRefFilter)
	kingpin.FatalIfError(err, "%s", errGetOrgReference)

	if orgRef[0].GUID == "" {
		kingpin.FatalIfError(fmt.Errorf("organization %s not found", orgName), "%s", errGetOrgReference)
	}
	
	// define filter-option with orgRef for query
	opt := &cfv3.SpaceListOptions{OrganizationGUIDs: cfv3.Filter{Values: []string{orgRef[0].GUID}}}

	// Get all spaces from CF
	responseCollection, err := c.client.Spaces.ListAll(ctx, opt)
	if err != nil {
		return nil, err
	}

	// Filter spaces by name and org-reference
	var results []interface{}
	var SSHlist []bool
	for _, space := range responseCollection {
		// Check if the space name matches
		if utils.IsFullMatch(name, space.Name){
			results = append(results, space)
			isSSHEnabled, err := c.client.SpaceFeatures.IsSSHEnabled(ctx, space.GUID)
			kingpin.FatalIfError(err, "%s", errIsSSHEnabled)
			SSHlist = append(SSHlist, isSSHEnabled)
		}
	}

	// Combine results and SSHlist into a slice of interfaces
	var combinedResults []interface{}
	for i := range results {
		combinedResults = append(combinedResults, map[string]interface{}{
			"result":   results[i],
			"SSH":  SSHlist[i],
		})
	}

	return combinedResults, nil
}

func (c *CFClient) getOrganizations(ctx context.Context, filter map[string]string) ([]interface{}, error) {
	// Get GUID filter
	name, ok := filter["name"]
	if !ok {
		return nil, fmt.Errorf("name filter is required for organizations")
	}

	// Get organizations from CF
	organizations, err :=  c.client.Organizations.ListAll(ctx, &cfv3.OrganizationListOptions{})
	kingpin.FatalIfError(err, "%s", errListOrganizations)

	// Filter organizations by name
	var results []interface{}
	for _, organization := range organizations {
		// Check if the organization name matches
		if utils.IsFullMatch(name, organization.Name) {
			results = append(results, organization)
		}
	}

	return results, nil
}

func (c *CFClient) getApps(ctx context.Context, filter map[string]string) ([]interface{}, error) {
	// Get name filter
	name, ok := filter["name"]
	if !ok {
		return nil, fmt.Errorf("name filter is required for apps")
	}
	spaceName, ok := filter["space"]
	if !ok {
		return nil, fmt.Errorf("org-reference filter is required for apps")
	}

	// get referenced space
	spaceRefFilter := cfv3.SpaceListOptions{Names: cfv3.Filter{Values: []string{spaceName}}}
	spaceRef, err := c.client.Spaces.ListAll(ctx, &spaceRefFilter)
	kingpin.FatalIfError(err, "%s", errGetSpaceReference)

	if spaceRef[0].GUID == "" {
		kingpin.FatalIfError(fmt.Errorf("organization %s not found", spaceName), "%s", errGetOrgReference)
	}

	// define filter-option with spaceRef for query
	opt := &cfv3.AppListOptions{SpaceGUIDs: cfv3.Filter{Values: []string{spaceRef[0].GUID}}}

	// Get apps from CF
	responseCollection, err := c.client.Applications.ListAll(ctx, opt)
	if err != nil {
		return nil, err
	}

	// Filter spaces by name and org-reference
	var results []interface{}
	for _, app := range responseCollection {
		// Check if the app name matches
		if utils.IsFullMatch(name, app.Name){
			results = append(results, app)
		}
	}

	return results, nil
}

// CFClientAdapter implements the ClientAdapter interface
type CFClientAdapter struct{}

func (a *CFClientAdapter) BuildClient(ctx context.Context, credentials client.Credentials) (client.ProviderClient, error) {
	cfCreds, ok := credentials.(*CFCredentials)
	config, err := cfconfig.New(cfCreds.ApiEndpoint, cfconfig.UserPassword(string(cfCreds.Email), string(cfCreds.Password)))
	kingpin.FatalIfError(err, "%s", errCreateCFConfig)

	if !ok {
		return nil, fmt.Errorf("invalid credentials type")
	}

	// Build CF client
	cfClientInstance, err := cfv3.New(config)
	if err != nil {
		return nil, err
	}

	return &CFClient{client: *cfClientInstance}, nil
}

func (a *CFClientAdapter) GetCredentials(ctx context.Context, kubeConfigPath string, providerConfigRef client.ProviderConfigRef, scheme *runtime.Scheme) (client.Credentials, error) {
	providerConfig := &v1beta1.ProviderConfig{}

	resourceRef := types.NamespacedName{
		Name:       providerConfigRef.Name,
		Namespace: providerConfigRef.Namespace,
	}

	k8sClient, err := kubernetes.NewK8sClient(kubeConfigPath, scheme)
	kingpin.FatalIfError(err, "%s", errCreateK8sClient)

	// Get the specific ProviderConfig resource and store it in providerConfig
	err = k8sClient.Get(ctx, resourceRef, providerConfig)
	kingpin.FatalIfError(err, "%s", errGetProviderConfig)

    secret := &corev1.Secret{}

    // Get the K8s-Secret and store in secret
    err = k8sClient.Get(ctx, types.NamespacedName{
		Name: providerConfig.Spec.Credentials.SecretRef.Name,
		Namespace: providerConfig.Spec.Credentials.SecretRef.Namespace,
	}, secret)
    kingpin.FatalIfError(err, "%s", errGetSecret)

	// Extract and decode the credentials JSON
	credentials, exists := secret.Data[providerConfig.Spec.Credentials.SecretRef.Key]
	if !exists {
		panic(errExtractCredentials)
	} 

	// CF Endpoint can be either directly in providerConfig or in a separate secret
	var apiEndpoint = ""
	if providerConfig.Spec.APIEndpoint != nil {
		// Get the API endpoint from the provider config directly
		apiEndpoint = *providerConfig.Spec.APIEndpoint
	} else {
		// Get the API endpoint from a secret
		apiSecret := &corev1.Secret{}
	
		// Get the K8s-Secret containing the CF-Endpoint and store in apiSecret
		err = k8sClient.Get(ctx, types.NamespacedName{
			Name: providerConfig.Spec.Endpoint.SecretRef.Name,
			Namespace: providerConfig.Spec.Endpoint.SecretRef.Namespace,
		}, apiSecret)
    	kingpin.FatalIfError(err, "%s", errGetSecret)

		apiEndpointRaw, exists := apiSecret.Data[providerConfig.Spec.Endpoint.SecretRef.Key]
		if !exists {
			panic(errExtractApiEndpoint)
		}
		apiEndpoint = string(apiEndpointRaw)
	}

	var creds CFCredentials
	err = json.Unmarshal(credentials, &creds)
	kingpin.FatalIfError(err, "%s", errUnmarshalCredentials)

	return &CFCredentials{
		ApiEndpoint: apiEndpoint,
		Email:       creds.Email,
		Password:    creds.Password,
	}, nil
}