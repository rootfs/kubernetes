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
	"time"

	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
)

type azureDiskAttacher struct {
	host    volume.VolumeHost
	manager azureManager
}

var _ volume.Attacher = &azureDiskAttacher{}

var _ volume.AttachableVolumePlugin = &azureDataDiskPlugin{}

const (
	checkSleepDuration = time.Second
)

func (plugin *azureDataDiskPlugin) NewAttacher() (volume.Attacher, error) {
	azure, err := getAzureDiskManager(plugin.host.GetCloudProvider())
	if err != nil {
		return nil, err
	}

	return &azureDiskAttacher{
		host:    plugin.host,
		manager: azure,
	}, nil
}

func (attacher *azureDiskAttacher) Attach(spec *volume.Spec, hostName string) (string, error) {
	volumeSource, err := getVolumeSource(spec)
	if err != nil {
		return "", err
	}
	instanceid, err := attacher.manager.InstanceID(hostName)
	if err != nil {
		return "", fmt.Errorf("failed to get azure instance id for host %q", hostName)
	}

	lun, err := attacher.manager.GetDiskLun(volumeSource.DiskName, volumeSource.DataDiskURI, instanceid)
	if err == cloudprovider.InstanceNotFound {
		// Log error and continue with attach
		glog.Warningf(
			"Error checking if volume is already attached to current node (%q). Will continue and try attach anyway. err=%v",
			instanceid, err)
	}

	if err == nil {
		// Volume is already attached to node.
		glog.Infof("Attach operation is successful. volume %q is already attached to node %q at lun %d.", volumeSource.DataDiskURI, instanceid, lun)

	} else {
		err = attacher.manager.AttachDisk(volumeSource.DiskName, volumeSource.DataDiskURI, instanceid, compute.CachingTypes(volumeSource.CachingMode))
		if err == nil {
			glog.Infof("Attach operation successful: volume %q attached to node %q.", volumeSource.DataDiskURI, instanceid)
			lun, err = attacher.manager.GetDiskLun(volumeSource.DiskName, volumeSource.DataDiskURI, instanceid)
			if err != nil {
				glog.Warningf(
					"Error getting LUN from volume %q. err=%v",
					volumeSource.DataDiskURI, err)
			}
			// should reach here
			// detach disk and return
			attacher.manager.DetachDiskByName(volumeSource.DiskName, volumeSource.DataDiskURI, instanceid)
			return "", fmt.Errorf("failed to get LUN after attach: volume %q node %q", volumeSource.DataDiskURI, instanceid)
		} else {
			glog.Infof("Attach volume %q to instance %q failed with %v", volumeSource.DataDiskURI, instanceid, err)
			return "", fmt.Errorf("Attach volume %q to instance %q failed with %v", volumeSource.DataDiskURI, instanceid, err)
		}
	}

	devicePath := findDiskByLun(int(lun), &osIOHandler{})
	if devicePath != "" {
		glog.Infof("Failed to find LUN %d on instance %q", lun, instanceid)
		return "", fmt.Errorf("Failed to find LUN %d on instance %q", lun, instanceid)
	}

	return devicePath, err
}

func (attacher *azureDiskAttacher) WaitForAttach(spec *volume.Spec, devicePath string, timeout time.Duration) (string, error) {
	return "", nil
}

func (attacher *azureDiskAttacher) GetDeviceMountPath(
	spec *volume.Spec) (string, error) {
	return "", nil
}

func (attacher *azureDiskAttacher) MountDevice(spec *volume.Spec, devicePath string, deviceMountPath string) error {
	return nil
}

type azureDiskDetacher struct {
	mounter mount.Interface
}

var _ volume.Detacher = &azureDiskDetacher{}

func (plugin *azureDataDiskPlugin) NewDetacher() (volume.Detacher, error) {
	return &azureDiskDetacher{
		mounter: plugin.host.GetMounter(),
	}, nil
}

func (detacher *azureDiskDetacher) Detach(deviceMountPath string, hostName string) error {
	return nil
}

func (detacher *azureDiskDetacher) WaitForDetach(devicePath string, timeout time.Duration) error {
	return nil
}

func (detacher *azureDiskDetacher) UnmountDevice(deviceMountPath string) error {
	return nil
}
