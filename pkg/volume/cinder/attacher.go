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

package cinder

import (
	"fmt"
	"os"
	"path"
	"time"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/util/exec"
	"k8s.io/kubernetes/pkg/util/keymutex"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
)

type cinderDiskAttacher struct{
	host volume.VolumeHost
	spec *volume.Spec
}

var _ volume.Attacher = &cinderDiskAttacher{}

var _ volume.AttachableVolumePlugin = &cinderPlugin{}

const (
	checkSleepDuration  = time.Second
)

// Singleton key mutex for keeping attach/detach operations for the same PD atomic
var attachDetachMutex = keymutex.NewKeyMutex()

func (plugin *cinderPlugin) NewAttacher(spec *volume.Spec, pod *api.Pod) (volume.Attacher, error) {
	return &cinderDiskAttacher{host: plugin.host,
		spec: spec,
	}, nil
}

func (attacher *cinderDiskAttacher) Attach(hostName string, mounter mount.Interface) error {
	volumeSource, _:= getVolumeSource(attacher.spec)
	VolumeID := volumeSource.VolumeID

	// Block execution until any pending detach operations for this PD have completed
	attachDetachMutex.LockKey(VolumeID)
	defer attachDetachMutex.UnlockKey(VolumeID)

	cloud, err := getCloudProvider(attacher.host.GetCloudProvider())
	if err != nil {
		return err
	}
	instanceid, err := cloud.InstanceID()
	if err != nil {
		return err
	}
	_, err = cloud.AttachDisk(instanceid, VolumeID)
	return err
}

func (attacher *cinderDiskAttacher) WaitForAttach(timeout time.Duration) (string, error) {
	cloud, err := getCloudProvider(attacher.host.GetCloudProvider())
	if err != nil {
		return "", err
	}
	volumeSource, _ := getVolumeSource(attacher.spec)
	VolumeID := volumeSource.VolumeID
	instanceid, err := cloud.InstanceID()
	if err != nil {
		return "", err
	}
	devicePath, err := cloud.GetAttachmentDiskPath(instanceid, VolumeID) 
	if err != nil {
		return "", err
	}

	ticker := time.NewTicker(checkSleepDuration)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		probeAttachedVolume()
		select {
		case <-ticker.C:
			glog.V(5).Infof("Checking Cinder disk %q is attached.", VolumeID)
			probeAttachedVolume()
			exists, err := pathExists(devicePath)
			if exists && err == nil {
				glog.Infof("Successfully found attached Cinder disk %q.", VolumeID)
				return devicePath, nil
			} else {
				//Log error, if any, and continue checking periodically
				glog.Errorf("Error Stat Cinder disk (%q) is attached: %v", VolumeID, err)
			} 
		case <-timer.C:
			return "", fmt.Errorf("Could not find attached Cinder disk %q. Timeout waiting for mount paths to be created.", VolumeID)
		}
	}
}

func (attacher *cinderDiskAttacher) GetDeviceMountPath() string {
	volumeSource, _ := getVolumeSource(attacher.spec)
	return makeGlobalPDName(attacher.host, volumeSource.VolumeID)
}

// FIXME: this method can be further pruned.
func (attacher *cinderDiskAttacher) MountDevice(devicePath string, deviceMountPath string, mounter mount.Interface) error {
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

	volumeSource, readOnly := getVolumeSource(attacher.spec)

	options := []string{}
	if readOnly {
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

type cinderDiskDetacher struct{
	host volume.VolumeHost
}

var _ volume.Detacher = &cinderDiskDetacher{}

func (plugin *cinderPlugin) NewDetacher() (volume.Detacher, error) {
	return &cinderDiskDetacher{host: plugin.host}, nil
}

func (detacher *cinderDiskDetacher) Detach(deviceMountPath string, hostName string) error {
	VolumeID := path.Base(deviceMountPath)

	// Block execution until any pending attach/detach operations for this PD have completed
	attachDetachMutex.LockKey(VolumeID)
	defer attachDetachMutex.UnlockKey(VolumeID)

	cloud, err := getCloudProvider(detacher.host.GetCloudProvider())
	if err != nil {
		return err
	}

	if err = cloud.DetachDisk(VolumeID, hostName); err != nil {
		glog.Errorf("Error detaching PD %q: %v", VolumeID, err)
		return err
	}
	return nil
}

func (detacher *cinderDiskDetacher) WaitForDetach(devicePath string, timeout time.Duration) error {
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
			return fmt.Errorf("Timeout reached; PD Device %v is still attached", devicePath)
		}
	}
}

func (detacher *cinderDiskDetacher) UnmountDevice(deviceMountPath string, mounter mount.Interface) error {
	volume := path.Base(deviceMountPath)
	if err := unmountPDAndRemoveGlobalPath(deviceMountPath, mounter); err != nil {
		glog.Errorf("Error unmounting %q: %v", volume, err)
	}

	return nil
}

func getVolumeSource(spec *volume.Spec) (*api.AWSElasticBlockStoreVolumeSource, bool) {
	var readOnly bool
	var volumeSource *api.AWSElasticBlockStoreVolumeSource

	if spec.Volume != nil && spec.Volume.AWSElasticBlockStore != nil {
		volumeSource = spec.Volume.AWSElasticBlockStore
		readOnly = volumeSource.ReadOnly
	} else {
		volumeSource = spec.PersistentVolume.Spec.AWSElasticBlockStore
		readOnly = spec.ReadOnly
	}

	return volumeSource, readOnly
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
	err := mounter.Unmount(globalMountPath)
	os.Remove(globalMountPath)
	return err
}
