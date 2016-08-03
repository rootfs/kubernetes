/*
Copyright 2016 The Kubernetes Authors.

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

package azure

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/cloudprovider"
)

// attach a vhd to vm
// the vhd must exist, can be identified by diskName, diskUri, and lun.
func (az *Cloud) AttachDisk(diskName, diskUri, vmName string, lun int32, cachingMode compute.CachingTypes) error {
	vm, exists, err := az.getVirtualMachine(vmName)
	if err != nil {
		return err
	} else if !exists {
		return cloudprovider.InstanceNotFound
	}
	disks := *vm.Properties.StorageProfile.DataDisks
	disks = append(disks,
		compute.DataDisk{
			Name: &diskName,
			Vhd: &compute.VirtualHardDisk{
				URI: &diskUri,
			},
			Lun:          &lun,
			Caching:      cachingMode,
			CreateOption: "attach",
		})

	newVM := compute.VirtualMachine{
		Location: vm.Location,
		Properties: &compute.VirtualMachineProperties{
			StorageProfile: &compute.StorageProfile{
				DataDisks: &disks,
			},
		},
	}
	glog.V(4).Infof("azure attaching disk %s[%q] lun %d", diskName, diskUri, lun)
	res, err := az.VirtualMachinesClient.CreateOrUpdate(az.ResourceGroup, vmName,
		newVM, nil)
	glog.V(4).Infof("azure attach result:%#v, err: %v", res, err)
	return err
}

// detach a vhd from host
// the vhd can be identified by diskName or diskUri
func (az *Cloud) DetachDiskByName(diskName, diskUri, vmName string) error {
	vm, exists, err := az.getVirtualMachine(vmName)
	if err != nil {
		return err
	} else if !exists {
		return cloudprovider.InstanceNotFound
	}
	disks := *vm.Properties.StorageProfile.DataDisks
	for i, disk := range disks {
		if (disk.Name != nil && diskName != "" && *disk.Name == diskName) ||
			(disk.Vhd.URI != nil && diskUri != "" && *disk.Vhd.URI == diskUri) {
			// found the disk
			glog.V(4).Infof("detach disk: lun %d name %q uri %q", *disk.Lun, diskName, diskUri)
			disks = append(disks[:i], disks[i+1:]...)
			break
		}
	}
	newVM := compute.VirtualMachine{
		Location: vm.Location,
		Properties: &compute.VirtualMachineProperties{
			StorageProfile: &compute.StorageProfile{
				DataDisks: &disks,
			},
		},
	}
	res, err := az.VirtualMachinesClient.CreateOrUpdate(az.ResourceGroup, vmName,
		newVM, nil)
	glog.V(4).Infof("azure detach result:%#v, err: %v", res, err)
	return err
}

// given a vhd's diskName and diskUri, find the lun on the host that the vhd is attached to
func (az *Cloud) GetDiskLun(diskName, diskUri, vmName string) (int32, error) {
	vm, exists, err := az.getVirtualMachine(vmName)
	if err != nil {
		return -1, err
	} else if !exists {
		return -1, cloudprovider.InstanceNotFound
	}
	disks := *vm.Properties.StorageProfile.DataDisks
	for _, disk := range disks {
		if disk.Lun != nil {
			if (disk.Name != nil && diskName != "" && *disk.Name == diskName) ||
				(disk.Vhd.URI != nil && diskUri != "" && *disk.Vhd.URI == diskUri) {
				// found the disk
				glog.V(4).Infof("find disk: lun %d name %q uri %q", *disk.Lun, diskName, diskUri)
				return *disk.Lun, nil
			}
		}
	}
	return -1, cloudprovider.VolumeNotFound
}

// search all vhd attachment on the host and find unused lun
// return -1 if all luns are used
func (az *Cloud) GetNextDiskLun(vmName string) (int32, error) {
	vm, exists, err := az.getVirtualMachine(vmName)
	if err != nil {
		return -1, err
	} else if !exists {
		return -1, cloudprovider.InstanceNotFound
	}
	used := make([]bool, 64)
	disks := *vm.Properties.StorageProfile.DataDisks
	for _, disk := range disks {
		if disk.Lun != nil {
			used[*disk.Lun] = true
		}
	}
	for k, v := range used {
		if !v {
			return int32(k), nil
		}
	}
	return -1, cloudprovider.VolumeNotFound
}

// Create a VHD blob
func (az *Cloud) CreateVolume(name, storageType, location string, requestGB int) (string, string, int, error) {
	// find a storage account
	accounts, err := az.getStorageAccounts()
	if err != nil {
		// TODO: create a storage account and container
		return "", "", 0, err
	}
	for _, account := range accounts {
		// glog.V(4).Infof("account %s type %s location %s", account.Name, account.StorageType, account.Location)
		if (storageType != "" && account.StorageType == storageType) && (location != "" && account.Location == location) {
			// find the access key with this account
			key, err := az.getStorageAccesskey(account.Name)
			if err != nil {
				glog.V(2).Infof("no key found for storage account %s", account.Name)
				continue
			}

			// creaet a page blob in this account's vhd container
			name, uri, err := az.createVhdBlob(account.Name, key, name, int64(requestGB), nil)
			if err != nil {
				glog.V(2).Infof("failed to create vhd in account %s: %v", account.Name, err)
				continue
			}
			glog.V(4).Infof("created vhd blob uri: %s", uri)
			return name, uri, requestGB, err
		}
	}
	return "", "", 0, fmt.Errorf("failed to find a matching storage account")
}

// Delete a VHD blob
func (az *Cloud) DeleteVolume(name, uri string) error {
	accountName, blob, err := az.getBlobNameAndAccountFromURI(uri)
	if err != nil {
		return fmt.Errorf("failed to parse vhd URI %v", err)
	}
	// find a storage account
	accounts, err := az.getStorageAccounts()
	if err != nil {
		glog.V(2).Infof("no storage accounts found")
		return err
	}
	for _, account := range accounts {
		if accountName == account.Name {
			key, err := az.getStorageAccesskey(account.Name)
			if err != nil {
				return fmt.Errorf("no key for storage account %s", account.Name)
			}

			err = az.deleteVhdBlob(account.Name, key, blob)
			glog.V(4).Infof("delete blob %s err: %v", uri, err)
			return err
		}
	}
	return fmt.Errorf("failed to find storage account for vhd %v, account %s, blob %s", uri, accountName, blob)
}
