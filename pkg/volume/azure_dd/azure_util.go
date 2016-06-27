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

package azure_dd

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/volume"
)

// operation and info on Azure Data Disk
type AzureDataDiskOp struct {
	// attach (true) or detach (false)
	attach bool
	// when detach, use lun to locate data disk
	lun int32
	// disk name
	name string
	// vhd uri
	uri string
	// caching type
	caching compute.CachingTypes
}

// Abstract interface to azure client operations.
type azureUtil interface {
	GetAzureSecret(host volume.VolumeHost, nameSpace, secretName string) (map[string]string, error)
	UpdateVMDataDisks(map[string]string, string) error
}

type azureSvc struct{}

func (s *azureSvc) GetAzureSecret(host volume.VolumeHost, nameSpace, secretName string) (map[string]string, error) {
	var clientId, clientSecret, subId, tenantId, resourceGroupName string
	kubeClient := host.GetKubeClient()
	if kubeClient == nil {
		return nil, fmt.Errorf("Cannot get kube client")
	}

	keys, err := kubeClient.Core().Secrets(nameSpace).Get(secretName)
	if err != nil {
		return nil, fmt.Errorf("Couldn't get secret %v/%v", nameSpace, secretName)
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
	m["resourceGroup"] = resourceGroupName
	m["tenantID"] = tenantId
	return m, nil
}

func (s *azureSvc) UpdateVMDataDisks(c map[string]string, op AzureDataDiskOp, vmName string) error {
	oauthConfig, err := azure.PublicCloud.OAuthConfigForTenant(c["tenantID"])
	if err != nil {
		return err
	}
	token, err := azure.NewServicePrincipalToken(*oauthConfig, c["clientID"], c["clientSecret"], azure.PublicCloud.ServiceManagementEndpoint)
	if err != nil {
		return err
	}
	client := compute.NewVirtualMachinesClient(c["subscriptionID"])
	client.Authorizer = token
	vm, err := client.Get(c["resourceGroup"], vmName, "")
	if err != nil {
		return err
	}
	disks := *vm.Properties.StorageProfile.DataDisks
	if op.attach {
		disks = append(disks,
			compute.DataDisk{
				Name: &op.name,
				Vhd: &compute.VirtualHardDisk{
					URI: &op.uri,
				},
				Caching: op.caching,
			})
	} else { // detach
		d := make([]compute.DataDisk, len(disks))
		for _, disk := range disks {
			if disk.Lun != nil && *disk.Lun == op.lun {
				// found a disk to detach
				glog.V(2).Infof("detach disk %#v", disk)
				continue
			}
			d = append(d, disk)
		}
		disks = d
	}
	newVM := compute.VirtualMachine{
		Location: vm.Location,
		Properties: &compute.VirtualMachineProperties{
			StorageProfile: &compute.StorageProfile{
				DataDisks: &disks,
			},
		},
	}
	res, err := client.CreateOrUpdate(c["resourceGroup"], vmName,
		newVM, nil)
	glog.V(2).Info("azure VM CreateOrUpdate result:%#v", res)
	return err
}
