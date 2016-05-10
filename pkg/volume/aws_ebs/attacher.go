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

package aws_ebs

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/util/exec"
	"k8s.io/kubernetes/pkg/util/keymutex"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
)

type awsElasticBlockStoreAttacher struct {
	host volume.VolumeHost
}

var _ volume.Attacher = &awsElasticBlockStoreAttacher{}

var _ volume.AttachableVolumePlugin = &awsElasticBlockStorePlugin{}

// Singleton key mutex for keeping attach/detach operations for the same PD atomic
var attachDetachMutex = keymutex.NewKeyMutex()

func (plugin *awsElasticBlockStorePlugin) NewAttacher() (volume.Attacher, error) {
	return &awsElasticBlockStoreAttacher{host: plugin.host}, nil
}

func (plugin *awsElasticBlockStorePlugin) GetDeviceName(spec *volume.Spec) (string, error) {
	volumeSource, _ := getVolumeSource(spec)
	if volumeSource == nil {
		return "", fmt.Errorf("Spec does not reference an EBS volume type")
	}

	return volumeSource.VolumeID, nil
}

func (plugin *awsElasticBlockStorePlugin) GetUniqueVolumeName(spec *volume.Spec) (string, error) {
	volumeSource, _ := getVolumeSource(spec)
	if volumeSource == nil {
		return "", fmt.Errorf("Spec does not reference an EBS volume type")
	}
	partition := ""
	if volumeSource.Partition != 0 {
		partition = strconv.Itoa(int(volumeSource.Partition))
	}
	return fmt.Sprintf("%s/%s-%s:%v", awsElasticBlockStorePluginName, volumeSource.VolumeID, partition, volumeSource.ReadOnly), nil
}

func (attacher *awsElasticBlockStoreAttacher) Attach(spec *volume.Spec, hostName string) error {
	volumeSource, readOnly := getVolumeSource(spec)
	VolumeID := volumeSource.VolumeID

	// Block execution until any pending detach operations for this PD have completed
	attachDetachMutex.LockKey(VolumeID)
	defer attachDetachMutex.UnlockKey(VolumeID)

	awsCloud, err := getCloudProvider(attacher.host.GetCloudProvider())
	if err != nil {
		return err
	}

	for numRetries := 0; numRetries < maxRetries; numRetries++ {
		if numRetries > 0 {
			glog.Warningf("Retrying attach for AWS PD %q (retry count=%v).", VolumeID, numRetries)
		}

		if _, err = awsCloud.AttachDisk(VolumeID, hostName, readOnly); err != nil {
			glog.Errorf("Error attaching PD %q: %+v", VolumeID, err)
			time.Sleep(errorSleepDuration)
			continue
		}
		return nil
	}

	return err
}

// TODO: use metadata service instead of querying cloud provider to retrieve devicePath
func (attacher *awsElasticBlockStoreAttacher) WaitForAttach(spec *volume.Spec, timeout time.Duration) (string, error) {
	awsCloud, err := getCloudProvider(attacher.host.GetCloudProvider())
	if err != nil {
		return "", err
	}
	volumeSource, _ := getVolumeSource(spec)
	VolumeID := volumeSource.VolumeID
	partition := ""
	if volumeSource.Partition != 0 {
		partition = strconv.Itoa(int(volumeSource.Partition))
	}
	devicePath, err := awsCloud.GetDiskPath(VolumeID)
	if err != nil {
		return "", err
	}

	ticker := time.NewTicker(checkSleepDuration)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	devicePaths := getDiskByIdPaths(partition, devicePath)
	for {
		select {
		case <-ticker.C:
			glog.V(5).Infof("Checking AWS PD %q is attached.", VolumeID)
			path, err := verifyDevicePath(devicePaths)
			if err != nil {
				// Log error, if any, and continue checking periodically. See issue #11321
				glog.Errorf("Error verifying AWS PD (%q) is attached: %v", VolumeID, err)
			} else if path != "" {
				// A device path has successfully been created for the PD
				glog.Infof("Successfully found attached AWS PD %q.", VolumeID)
				return path, nil
			}
		case <-timer.C:
			return "", fmt.Errorf("Could not find attached AWS PD %q. Timeout waiting for mount paths to be created.", VolumeID)
		}
	}
}

func (attacher *awsElasticBlockStoreAttacher) GetDeviceMountPath(spec *volume.Spec) string {
	volumeSource, _ := getVolumeSource(spec)
	return makeGlobalPDPath(attacher.host, volumeSource.VolumeID)
}

// FIXME: this method can be further pruned.
func (attacher *awsElasticBlockStoreAttacher) MountDevice(spec *volume.Spec, devicePath string, deviceMountPath string, mounter mount.Interface) error {
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

	volumeSource, readOnly := getVolumeSource(spec)

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

type awsElasticBlockStoreDetacher struct {
	host volume.VolumeHost
}

var _ volume.Detacher = &awsElasticBlockStoreDetacher{}

func (plugin *awsElasticBlockStorePlugin) NewDetacher() (volume.Detacher, error) {
	return &awsElasticBlockStoreDetacher{host: plugin.host}, nil
}

func (detacher *awsElasticBlockStoreDetacher) Detach(deviceMountPath string, hostName string) error {
	VolumeID := path.Base(deviceMountPath)

	// Block execution until any pending attach/detach operations for this PD have completed
	attachDetachMutex.LockKey(VolumeID)
	defer attachDetachMutex.UnlockKey(VolumeID)

	awsCloud, err := getCloudProvider(detacher.host.GetCloudProvider())
	if err != nil {
		return err
	}

	for numRetries := 0; numRetries < maxRetries; numRetries++ {
		if numRetries > 0 {
			glog.Warningf("Retrying detach for AWS PD %q (retry count=%v).", VolumeID, numRetries)
		}

		if _, err = awsCloud.DetachDisk(VolumeID, hostName); err != nil {
			glog.Errorf("Error detaching PD %q: %v", VolumeID, err)
			time.Sleep(errorSleepDuration)
			continue
		}
		return nil
	}

	return err
}

func (detacher *awsElasticBlockStoreDetacher) WaitForDetach(devicePath string, timeout time.Duration) error {
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

func (detacher *awsElasticBlockStoreDetacher) UnmountDevice(deviceMountPath string, mounter mount.Interface) error {
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
