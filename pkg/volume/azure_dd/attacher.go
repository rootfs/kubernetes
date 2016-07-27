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
	"time"

	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
)

type azureDiskAttacher struct {
	host           volume.VolumeHost
}

var _ volume.Attacher = &azureDiskAttacher{}

var _ volume.AttachableVolumePlugin = &azureDataDiskPlugin{}

const (
	checkSleepDuration = time.Second
)

func (plugin *azureDataDiskPlugin) NewAttacher() (volume.Attacher, error) {
	return &azureDiskAttacher{
		host:           plugin.host,
	}, nil
}

func (attacher *azureDiskAttacher) Attach(spec *volume.Spec, hostName string) (string, error) {
	return "", nil
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
	mounter        mount.Interface
}

var _ volume.Detacher = &azureDiskDetacher{}

func (plugin *azureDataDiskPlugin) NewDetacher() (volume.Detacher, error) {
	return &azureDiskDetacher{
		mounter:        plugin.host.GetMounter(),
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
