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

package azure

import (
	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/cloudprovider"
)

func (az *Cloud) AttachDisk(diskName, diskUri, vmName string, cachingMode compute.CachingTypes) error {
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
	res, err := az.VirtualMachinesClient.CreateOrUpdate(az.ResourceGroup, vmName,
		newVM, nil)
	glog.V(4).Info("azure attach result:%#v", res)
	return err
}

func (az *Cloud) DetachDiskByLun(lun int32, vmName string) error {
	vm, exists, err := az.getVirtualMachine(vmName)
	if err != nil {
		return err
	} else if !exists {
		return cloudprovider.InstanceNotFound
	}
	disks := *vm.Properties.StorageProfile.DataDisks
	d := make([]compute.DataDisk, len(disks))
	for _, disk := range disks {
		if disk.Lun != nil && *disk.Lun == lun {
			// found a disk to detach
			glog.V(4).Infof("detach disk: lun %d name %q uri %q size(GB): %d\n", *disk.Lun, *disk.Name, *disk.Vhd.URI, *disk.DiskSizeGB)
			continue
		}
		d = append(d, disk)
	}
	disks = d

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
	glog.V(2).Info("azure detach result:%#v", res)
	return err
}

func (az *Cloud) DetachDiskByName(diskName, diskUri, vmName string) error {
	vm, exists, err := az.getVirtualMachine(vmName)
	if err != nil {
		return err
	} else if !exists {
		return cloudprovider.InstanceNotFound
	}
	disks := *vm.Properties.StorageProfile.DataDisks
	d := make([]compute.DataDisk, len(disks))
	for _, disk := range disks {
		if (disk.Name != nil && diskName != "" && *disk.Name == diskName) ||
			(disk.Vhd.URI != nil && diskUri != "" && *disk.Vhd.URI == diskUri) {
			// found the disk
			glog.V(4).Infof("detach disk: lun %d name %q uri %q size(GB): %d\n", *disk.Lun, *disk.Name, *disk.Vhd.URI, *disk.DiskSizeGB)
			continue
		}
		d = append(d, disk)
	}
	disks = d

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
	glog.V(2).Info("azure detach result:%#v", res)
	return err
}

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
				glog.V(4).Infof("find disk: lun %d name %q uri %q size(GB): %d\n", *disk.Lun, *disk.Name, *disk.Vhd.URI, *disk.DiskSizeGB)
				return *disk.Lun, nil
			}
		}
	}
	return -1, cloudprovider.VolumeNotFound
}
