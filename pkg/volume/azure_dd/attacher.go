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
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/util/exec"
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
	glog.Infof("debug: new attacher")
	azure, err := getAzureDiskManager(plugin.host.GetCloudProvider())
	if err != nil {
		glog.Infof("failed to get azure provider")
		return nil, err
	}

	return &azureDiskAttacher{
		host:    plugin.host,
		manager: azure,
	}, nil
}

func (attacher *azureDiskAttacher) Attach(spec *volume.Spec, hostName string) (string, error) {
	glog.Infof("debug: attach")
	volumeSource, err := getVolumeSource(spec)
	if err != nil {
		glog.Infof("failed to get azure disk spec")
		return "", err
	}
	instanceid, err := attacher.manager.InstanceID(hostName)
	if err != nil {
		glog.Infof("failed to get azure instance id")
		return "", fmt.Errorf("failed to get azure instance id for host %q", hostName)
	}
	if ind := strings.LastIndex(instanceid, "/"); ind >= 0 {
		instanceid = instanceid[(ind + 1):]
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
		glog.Infof("Attach operation is successful. volume %q is already attached to node %q at lun %d.", volumeSource.DiskName, instanceid, lun)

	} else {
		glog.Infof("debug: attaching disk")
		lun, err = attacher.manager.GetNextDiskLun(instanceid)
		if err != nil {
			glog.Warningf("no LUN available for instance %q", instanceid)
			return "", fmt.Errorf("all LUNs are used, cannot attach volume %q to instance %q", volumeSource.DiskName, instanceid)
		}
		glog.Infof("debug: attaching lun %v", lun)

		err = attacher.manager.AttachDisk(volumeSource.DiskName, volumeSource.DataDiskURI, instanceid, lun, compute.CachingTypes(volumeSource.CachingMode))
		if err == nil {
			glog.Infof("Attach operation successful: volume %q attached to node %q.", volumeSource.DataDiskURI, instanceid)
		} else {
			glog.Infof("Attach volume %q to instance %q failed with %v", volumeSource.DataDiskURI, instanceid, err)
			return "", fmt.Errorf("Attach volume %q to instance %q failed with %v", volumeSource.DiskName, instanceid, err)
		}
	}

	glog.Infof("debug: lun %d attached", lun)
	return strconv.Itoa(int(lun)), err
}

func (attacher *azureDiskAttacher) WaitForAttach(spec *volume.Spec, dev string, timeout time.Duration) (string, error) {
	glog.Infof("debug: wait for attach")
	volumeSource, err := getVolumeSource(spec)
	if err != nil {
		return "", err
	}

	if dev == "" {
		return "", fmt.Errorf("WaitForAttach failed for Azure disk %q: devicePath is empty.", volumeSource.DiskName)
	}
	glog.V(4).Infof("wait for lun %q", dev)
	lun, err := strconv.Atoi(dev)
	if err != nil {
		return "", fmt.Errorf("WaitForAttach: wrong lun %q", dev)
	}
	devicePath := findDiskByLun(lun, &osIOHandler{})
	if devicePath == "" {
		return "", fmt.Errorf("cannot find device for lun %q", dev)
	}
	ticker := time.NewTicker(checkSleepDuration)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ticker.C:
			glog.V(5).Infof("Checking Azure disk %q is attached.", volumeSource.DiskName)
			exists, err := pathExists(devicePath)
			if exists && err == nil {
				glog.Infof("Successfully found attached Azure disk %q.", volumeSource.DiskName)
				return devicePath, nil
			} else {
				//Log error, if any, and continue checking periodically
				glog.Errorf("Error Stat Azure disk (%q) is attached: %v", volumeSource.DiskName, err)
			}
		case <-timer.C:
			return "", fmt.Errorf("Could not find attached Azure disk %q. Timeout waiting for mount paths to be created.", volumeSource.DiskName)
		}
	}
}

func (attacher *azureDiskAttacher) GetDeviceMountPath(
	spec *volume.Spec) (string, error) {
	glog.Infof("debug: get device mount path")
	volumeSource, err := getVolumeSource(spec)
	if err != nil {
		return "", err
	}

	return makeGlobalPDPath(attacher.host, volumeSource.DiskName), nil

}

func (attacher *azureDiskAttacher) MountDevice(spec *volume.Spec, devicePath string, deviceMountPath string) error {
	glog.Infof("debug: mount device")
	mounter := attacher.host.GetMounter()
	notMnt, err := mounter.IsLikelyNotMountPoint(deviceMountPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(deviceMountPath, 0750); err != nil {
				return err
			}
			notMnt = true
		} else {
			return err
		}
	}

	volumeSource, err := getVolumeSource(spec)
	if err != nil {
		return err
	}

	options := []string{}
	if spec.ReadOnly {
		options = append(options, "ro")
	}
	if notMnt {
		diskMounter := &mount.SafeFormatAndMount{Interface: mounter, Runner: exec.New()}
		err = diskMounter.FormatAndMount(devicePath, deviceMountPath, volumeSource.FSType, options)
		if err != nil {
			os.Remove(deviceMountPath)
			return err
		}
	}
	return nil
}

type azureDiskDetacher struct {
	mounter mount.Interface
	manager azureManager
}

var _ volume.Detacher = &azureDiskDetacher{}

func (plugin *azureDataDiskPlugin) NewDetacher() (volume.Detacher, error) {
	azure, err := getAzureDiskManager(plugin.host.GetCloudProvider())
	if err != nil {
		return nil, err
	}

	return &azureDiskDetacher{
		mounter: plugin.host.GetMounter(),
		manager: azure,
	}, nil
}

func (detacher *azureDiskDetacher) Detach(dev string, hostName string) error {
	glog.Infof("debug: detach %v", dev)
	if dev == "" {
		return fmt.Errorf("invalid dev to detach: %q", dev)
	}
	instanceid, err := detacher.manager.InstanceID(hostName)
	if err != nil {
		return fmt.Errorf("failed to get azure instance id for host %q", hostName)
	}
	if ind := strings.LastIndex(instanceid, "/"); ind >= 0 {
		instanceid = instanceid[(ind + 1):]
	}

	err = detacher.manager.DetachDiskByName(dev, "", instanceid)
	if err != nil {
		glog.V(2).Infof("failed to detach azure disk %q", dev)
	}
	return err
}

func (detacher *azureDiskDetacher) WaitForDetach(devicePath string, timeout time.Duration) error {
	glog.Infof("debug: watch for detach %v", devicePath)
	ticker := time.NewTicker(checkSleepDuration)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ticker.C:
			glog.V(5).Infof("Checking device %q is detached.", devicePath)
			if pathExists, err := pathExists(devicePath); err != nil {
				return fmt.Errorf("Error checking if device path exists: %v", err)
			} else if !pathExists {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("Timeout reached; Device %v is still attached", devicePath)
		}
	}
}

func (detacher *azureDiskDetacher) UnmountDevice(deviceMountPath string) error {
	volume := path.Base(deviceMountPath)
	if err := unmountPDAndRemoveGlobalPath(deviceMountPath, detacher.mounter); err != nil {
		glog.Errorf("Error unmounting %q: %v", volume, err)
	}

	return nil

}

// Checks if the specified path exists
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

// Unmount the global mount path, which should be the only one, and delete it.
func unmountPDAndRemoveGlobalPath(globalMountPath string, mounter mount.Interface) error {
	if pathExists, pathErr := pathExists(globalMountPath); pathErr != nil {
		return fmt.Errorf("Error checking if path exists: %v", pathErr)
	} else if !pathExists {
		glog.V(5).Infof("Unmount skipped because path does not exist: %v", globalMountPath)
		return nil
	}
	err := mounter.Unmount(globalMountPath)
	os.Remove(globalMountPath)
	return err
}
