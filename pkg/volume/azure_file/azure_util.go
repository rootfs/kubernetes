/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

	azure "github.com/Azure/azure-sdk-for-go/storage"
	"k8s.io/kubernetes/pkg/volume"
)

// Abstract interface to azure file operations.
type azureUtil interface {
	SetupAzureFileSvc(host volume.VolumeHost, nameSpace, keyName, accountName, shareName string) (string, error)
}

type azureSvc struct{}

func (s *azureSvc) SetupAzureFileSvc(host volume.VolumeHost, nameSpace, keyName, accountName, shareName string) (string, error) {
	var accountKey string
	kubeClient := host.GetKubeClient()
	if kubeClient == nil {
		return "", fmt.Errorf("Cannot get kube client")
	}

	keys, err := kubeClient.Secrets(nameSpace).Get(keyName)
	if err != nil {
		return "", fmt.Errorf("Couldn't get account key %v/%v", nameSpace, keyName)
	}
	for _, data := range keys.Data {
		accountKey = string(data)
	}

	//create azure client instance
	client, err := azure.NewBasicClient(accountName, accountKey)
	if err != nil {
		return "", fmt.Errorf("failed to create azure storage client: %v", err)
	}
	svc := client.GetFileService()
	//create azure file share if not exists yet
	if _, err := svc.CreateShareIfNotExists(shareName); err != nil {
		return "", fmt.Errorf("failed to create azure file share(%s): %v", shareName, err)
	}
	return accountKey, nil
}
