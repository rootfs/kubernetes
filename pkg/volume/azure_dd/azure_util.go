/*
Copyright 2016 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azure_file

import (
	"fmt"

	azcompute "github.com/Azure/azure-sdk-for-go/arm/compute"
	azhelpers "github.com/Azure/azure-sdk-for-go/arm/examples/helpers"
	"github.com/Azure/go-autorest/autorest/azure"
	"k8s.io/kubernetes/pkg/volume"
)

// Abstract interface to azure client operations.
type azureUtil interface {
	GetAzureCredentials(host volume.VolumeHost, nameSpace, secretName string) (map[string]string, error)
	GetAzurePrincipalToken(map[string]string, string)(*azure.ServicePrincipalToken, err)
}

type azureSvc struct{}

func (s *azureSvc) GetAzureCredentials(host volume.VolumeHost, nameSpace, secretName string) (map[string]string, error) {
	var clientId, clientSecret, subId, tenantId, resourceGroupName string
	kubeClient := host.GetKubeClient()
	if kubeClient == nil {
		return "", "", fmt.Errorf("Cannot get kube client")
	}

	keys, err := kubeClient.Core().Secrets(nameSpace).Get(secretName)
	if err != nil {
		return "", "", fmt.Errorf("Couldn't get secret %v/%v", nameSpace, secretName)
	}
	for name, data := range keys.Data {
		if name == "azureclientid" {
			clientId = string(data)
		}
		if name == "azureclientsecret" {
			clientSecret = string(data)
		}
		if name == "azuresubscriptionid" {
			subId = string(data)
		}
		if name == "azureresourcegroupname" {
			resourceGroupName = string(data)
		}
		if name == "azuretenantid" {
			tenantId = string(data)
		}
	}
	if clientId == "" || clientSecret == "" ||
		subId == "" || resourceGroupName == "" || tenantId == "" {
		return nil, fmt.Errorf("Invalid %v/%v: Not all keys can be found", nameSpace, secretName)
	}
	m := make(map[string]string)
	m["clientID"] = clientId
	m["clientSecret"] = clientSecret
	m["subscriptionId"] = subId
	m["resourceGroup"] = resourceGroupname
	m["tenantID"] = tenantId
	return m, nil
}

func (s *azureSvc) GetAzurePrincipalToken(m map[string]string, scope string)(*azure.ServicePrincipalToken, err){
	oauthConfig, err := azure.PublicCloud.OAuthConfigForTenant(c["tenantID"])
	if err != nil {
		return nil, err
	}
	return azure.NewServicePrincipalToken(*oauthConfig, c["clientID"], c["clientSecret"], scope)
}
