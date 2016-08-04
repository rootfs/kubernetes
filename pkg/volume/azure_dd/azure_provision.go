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

package azure_dd

import (
	"fmt"

	"github.com/golang/glog"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	utilstrings "k8s.io/kubernetes/pkg/util/strings"
	"k8s.io/kubernetes/pkg/volume"
)

var _ volume.DeletableVolumePlugin = &azureDataDiskPlugin{}
var _ volume.ProvisionableVolumePlugin = &azureDataDiskPlugin{}

type azureDiskDeleter struct {
	*azureDisk
	manager azureManager
}

func (plugin *azureDataDiskPlugin) NewDeleter(spec *volume.Spec) (volume.Deleter, error) {
	azure, err := getAzureDiskManager(plugin.host.GetCloudProvider())
	if err != nil {
		glog.V(4).Infof("failed to get azure provider")
		return nil, err
	}

	return plugin.newDeleterInternal(spec, azure)
}

func (plugin *azureDataDiskPlugin) newDeleterInternal(spec *volume.Spec, azure azureManager) (volume.Deleter, error) {
	if spec.PersistentVolume != nil && spec.PersistentVolume.Spec.AzureDisk == nil {
		return nil, fmt.Errorf("invalid PV spec")
	}
	diskName := spec.PersistentVolume.Spec.AzureDisk.DiskName
	diskUri := spec.PersistentVolume.Spec.AzureDisk.DataDiskURI
	return &azureDiskDeleter{
		azureDisk: &azureDisk{
			volName:  spec.Name(),
			diskName: diskName,
			diskUri:  diskUri,
			plugin:   plugin,
		},
		manager: azure,
	}, nil
}

func (plugin *azureDataDiskPlugin) NewProvisioner(options volume.VolumeOptions) (volume.Provisioner, error) {
	azure, err := getAzureDiskManager(plugin.host.GetCloudProvider())
	if err != nil {
		glog.V(4).Infof("failed to get azure provider")
		return nil, err
	}
	if len(options.AccessModes) == 0 {
		options.AccessModes = plugin.GetAccessModes()
	}
	return plugin.newProvisionerInternal(options, azure)
}

func (plugin *azureDataDiskPlugin) newProvisionerInternal(options volume.VolumeOptions, azure azureManager) (volume.Provisioner, error) {
	return &azureDiskProvisioner{
		azureDisk: &azureDisk{
			plugin: plugin,
		},
		manager: azure,
		options: options,
	}, nil
}

var _ volume.Deleter = &azureDiskDeleter{}

func (d *azureDiskDeleter) GetPath() string {
	name := azureDataDiskPluginName
	return d.plugin.host.GetPodVolumeDir(d.podUID, utilstrings.EscapeQualifiedNameForDisk(name), d.volName)
}

func (d *azureDiskDeleter) Delete() error {
	glog.V(4).Infof("deleting volume %s", d.diskUri)
	return d.manager.DeleteVolume(d.diskName, d.diskUri)
}

type azureDiskProvisioner struct {
	*azureDisk
	manager azureManager
	options volume.VolumeOptions
}

var _ volume.Provisioner = &azureDiskProvisioner{}

func (a *azureDiskProvisioner) Provision() (*api.PersistentVolume, error) {
	name := volume.GenerateVolumeName(a.options.ClusterName, a.options.PVName, 255)
	requestBytes := a.options.Capacity.Value()
	requestGB := int(volume.RoundUpSize(requestBytes, 1024*1024*1024))
	// FIXME: get type, location from storage class once merged
	diskName, diskUri, sizeGB, err := a.manager.CreateVolume(name, "Standard_LRS", "eastus", requestGB)
	if err != nil {
		return nil, err
	}

	pv := &api.PersistentVolume{
		ObjectMeta: api.ObjectMeta{
			Name:   a.options.PVName,
			Labels: map[string]string{},
			Annotations: map[string]string{
				"kubernetes.io/createdby": "azure-disk-dynamic-provisioner",
			},
		},
		Spec: api.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: a.options.PersistentVolumeReclaimPolicy,
			AccessModes:                   a.options.AccessModes,
			Capacity: api.ResourceList{
				api.ResourceName(api.ResourceStorage): resource.MustParse(fmt.Sprintf("%dGi", sizeGB)),
			},
			PersistentVolumeSource: api.PersistentVolumeSource{
				AzureDisk: &api.AzureDiskVolumeSource{
					DiskName:    diskName,
					DataDiskURI: diskUri,
					FSType:      "ext4",
					ReadOnly:    false,
				},
			},
		},
	}
	return pv, nil
}
