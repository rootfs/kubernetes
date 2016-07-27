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
	"fmt"
	
	"k8s.io/kubernetes/pkg/cloudprovider"
	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/golang/glog"
)

type diskOp int

const (
	ATTACH diskOp = iota
	DETACH
	QUERY
)

// operation and info on Azure Data Disk
type AzureDataDiskOp struct {
	// attach/detach/query
	Action diskOp
	// when detach, use lun to locate data disk
	Lun int32
	// disk name
	Name string
	// vhd uri
	Uri string
	// caching type
	Caching compute.CachingTypes
}



// attach/detach/query vm's data disks
// to attach: op.action = ATTACH, op.name and op.uri must be set
// to detach: op.action = DETACH, op.lun must be set
// to query:  op.action = QUERY, op.name or op.uri must be set, op.lun is returned
func (az *Cloud) AzureDataDisksOp(op AzureDataDiskOp, vmName string) error {
	vm, exists, err := az.getVirtualMachine(vmName)
	if err != nil {
		return err
	} else if !exists {
		return cloudprovider.InstanceNotFound
	}
	disks := *vm.Properties.StorageProfile.DataDisks
	switch op.Action {
	case ATTACH:
		disks = append(disks,
			compute.DataDisk{
				Name: &op.Name,
				Vhd: &compute.VirtualHardDisk{
					URI: &op.Uri,
				},
				Caching: op.Caching,
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
		glog.V(2).Info("azure attach result:%#v", res)
		return err
	case DETACH:
		d := make([]compute.DataDisk, len(disks))
		for _, disk := range disks {
			if disk.Lun != nil && *disk.Lun == op.Lun {
				// found a disk to detach
				glog.V(2).Infof("detach disk: lun %d name %s uri %s size(GB): %d\n", *disk.Lun, *disk.Name, *disk.Vhd.URI, *disk.DiskSizeGB)
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
	case QUERY:
		for _, disk := range disks {
			if disk.Lun != nil {
				if (disk.Name != nil && op.Name != "" && *disk.Name == op.Name) ||
					(disk.Vhd.URI != nil && op.Uri != "" && *disk.Vhd.URI == op.Uri) {
					// found the disk
					glog.V(4).Infof("find disk: lun %d name %s uri %s size(GB): %d\n", *disk.Lun, *disk.Name, *disk.Vhd.URI, *disk.DiskSizeGB)
					op.Lun = *disk.Lun
					return nil
				}
			}
		}
		return fmt.Errorf("cannot find vhd with name %s and uri %s", op.Name, op.Uri)
	}
	return nil
}
