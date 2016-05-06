/*
Copyright 2014 The Kubernetes Authors All rights reserved.

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
	"path/filepath"
	"strconv"
	"strings"

	azcompute "github.com/Azure/azure-sdk-for-go/arm/compute"
	azhelpers "github.com/Azure/azure-sdk-for-go/arm/examples/helpers"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util/exec"
	"k8s.io/kubernetes/pkg/util/mount"
	utilstrings "k8s.io/kubernetes/pkg/util/strings"
	"k8s.io/kubernetes/pkg/volume"
)

// This is the primary entrypoint for volume plugins.
func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&azureDataDiskPlugin{nil}}
}

type azureDataDiskPlugin struct {
	host volume.VolumeHost
}

var _ volume.VolumePlugin = &azureDataDiskPlugin{}
var _ volume.PersistentVolumePlugin = &azureDataDiskPlugin{}
var _ volume.DeletableVolumePlugin = &azureDataDiskPlugin{}
var _ volume.ProvisionableVolumePlugin = &azureDataDiskPlugin{}

const (
	azureDataDiskPluginName = "kubernetes.io/azure-disk"
)

func (plugin *azureDataDiskPlugin) Init(host volume.VolumeHost) error {
	plugin.host = host
	return nil
}

func (plugin *azureDataDiskPlugin) Name() string {
	return azureDataDiskPluginName
}

func (plugin *azureDataDiskPlugin) CanSupport(spec *volume.Spec) bool {
	return (spec.PersistentVolume != nil && spec.PersistentVolume.Spec.AzureDisk != nil) ||
		(spec.Volume != nil && spec.Volume.AzureDisk != nil)
}

func (plugin *azureDataDiskPlugin) GetAccessModes() []api.PersistentVolumeAccessMode {
	return []api.PersistentVolumeAccessMode{
		api.ReadWriteOnce,
	}
}

func (plugin *azureDataDiskPlugin) NewMounter(spec *volume.Spec, pod *api.Pod, _ volume.VolumeOptions) (volume.Mounter, error) {
	// Inject real implementations here, test through the internal function.
	return plugin.newMounterInternal(spec, pod.UID, &AWSDiskUtil{}, plugin.host.GetMounter())
}

func (plugin *azureDataDiskPlugin) newMounterInternal(spec *volume.Spec, podUID types.UID, manager azureManager, mounter mount.Interface) (volume.Mounter, error) {
	// azures used directly in a pod have a ReadOnly flag set by the pod author.
	// azures used as a PersistentVolume gets the ReadOnly flag indirectly through the persistent-claim volume used to mount the PV
	var readOnly bool
	var azure *api.AzureDiskVolumeSource
	if spec.Volume != nil && spec.Volume.AzureDisk != nil {
		azure = spec.Volume.AzureDisk
		readOnly = azure.ReadOnly
	} else {
		azure = spec.PersistentVolume.Spec.AzureDisk
		readOnly = spec.ReadOnly
	}

	fsType := azure.FSType
	diskName := azure.DiskName
	diskUri := azure.DataDiskURI
	secretName := azure.SecretName
	cacheMode := azure.CacheMode
	partition := ""
	if azure.Partition != 0 {
		partition = strconv.Itoa(int(azure.Partition))
	}

	return &AzureDiskMounter{
		AzureDisk: &AzureDisk{
			podUID:     podUID,
			volName:    spec.Name(),
			secretName: secretName,
			diskName:   diskName,
			diskUri:    diskUri,
			cacheMode:  cacheMode,
			partition:  partition,
			manager:    manager,
			mounter:    mounter,
			plugin:     plugin,
		},
		fsType:      fsType,
		readOnly:    readOnly,
		diskMounter: &mount.SafeFormatAndMount{Interface: plugin.host.GetMounter(), Runner: exec.New()}}, nil
}

func (plugin *azureDataDiskPlugin) NewUnmounter(volName string, podUID types.UID) (volume.Unmounter, error) {
	// Inject real implementations here, test through the internal function.
	return plugin.newUnmounterInternal(volName, podUID, &AWSDiskUtil{}, plugin.host.GetMounter())
}

func (plugin *azureDataDiskPlugin) newUnmounterInternal(volName string, podUID types.UID, manager azureManager, mounter mount.Interface) (volume.Unmounter, error) {
	return &AzureDiskUnmounter{&AzureDisk{
		podUID:  podUID,
		volName: volName,
		manager: manager,
		mounter: mounter,
		plugin:  plugin,
	}}, nil
}

type AzureDisk struct {
	volName    string
	podUID     types.UID
	secretName string
	diskName   string
	diskUri    string
	cacheMode  string
	partition  string
	manager    azureDiskManager
	mounter    mount.Interface
	plugin     *azureDataDiskPlugin
	volume.MetricsNil
}

type AzureDiskMounter struct {
	*AzureDisk
	// Filesystem type, optional.
	fsType string
	// Specifies whether the disk will be attached as read-only.
	readOnly bool
	// diskMounter provides the interface that is used to mount the actual block device.
	diskMounter *mount.SafeFormatAndMount
}

var _ volume.Mounter = &AzureDiskMounter{}

func (b *AzureDiskMounter) GetAttributes() volume.Attributes {
	return volume.Attributes{
		ReadOnly:        b.readOnly,
		Managed:         !b.readOnly,
		SupportsSELinux: true,
	}
}

// SetUp attaches the disk and bind mounts to the volume path.
func (b *AzureDiskMounter) SetUp(fsGroup *int64) error {
	return b.SetUpAt(b.GetPath(), fsGroup)
}

// SetUpAt attaches the disk and bind mounts to the volume path.
func (b *AzureDiskMounter) SetUpAt(dir string, fsGroup *int64) error {
	// TODO: handle failed mounts here.
	notMnt, err := b.mounter.IsLikelyNotMountPoint(dir)
	glog.V(4).Infof("DataDisk set up: %s %v %v", dir, !notMnt, err)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !notMnt {
		return nil
	}

	globalPDPath := makeGlobalPDPath(b.plugin.host, b.volumeID)
	if err := b.manager.AttachAndMountDisk(b, globalPDPath); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0750); err != nil {
		// TODO: we should really eject the attach/detach out into its own control loop.
		detachDiskLogError(b.AzureDisk)
		return err
	}

	// Perform a bind mount to the full path to allow duplicate mounts of the same PD.
	options := []string{"bind"}
	if b.readOnly {
		options = append(options, "ro")
	}
	err = b.mounter.Mount(globalPDPath, dir, "", options)
	if err != nil {
		notMnt, mntErr := b.mounter.IsLikelyNotMountPoint(dir)
		if mntErr != nil {
			glog.Errorf("IsLikelyNotMountPoint check failed: %v", mntErr)
			return err
		}
		if !notMnt {
			if mntErr = b.mounter.Unmount(dir); mntErr != nil {
				glog.Errorf("Failed to unmount: %v", mntErr)
				return err
			}
			notMnt, mntErr := b.mounter.IsLikelyNotMountPoint(dir)
			if mntErr != nil {
				glog.Errorf("IsLikelyNotMountPoint check failed: %v", mntErr)
				return err
			}
			if !notMnt {
				// This is very odd, we don't expect it.  We'll try again next sync loop.
				glog.Errorf("%s is still mounted, despite call to unmount().  Will try again next sync loop.", dir)
				return err
			}
		}
		os.Remove(dir)
		return err
	}

	if !b.readOnly {
		volume.SetVolumeOwnership(b, fsGroup)
	}

	return nil
}

func makeGlobalPDPath(host volume.VolumeHost, volume string) string {
	return path.Join(host.GetPluginDir(azureDataDiskPluginName), "mounts", volume)
}

func (azure *AzureDisk) GetPath() string {
	name := azureDataDiskPluginName
	return azure.plugin.host.GetPodVolumeDir(azure.podUID, utilstrings.EscapeQualifiedNameForDisk(name), azure.volName)
}

type AzureDiskUnmounter struct {
	*AzureDisk
}

var _ volume.Unmounter = &AzureDiskUnmounter{}

// Unmounts the bind mount, and detaches the disk only if the PD
// resource was the last reference to that disk on the kubelet.
func (c *AzureDiskUnmounter) TearDown() error {
	return c.TearDownAt(c.GetPath())
}

// Unmounts the bind mount, and detaches the disk only if the PD
// resource was the last reference to that disk on the kubelet.
func (c *AzureDiskUnmounter) TearDownAt(dir string) error {
	notMnt, err := c.mounter.IsLikelyNotMountPoint(dir)
	if err != nil {
		glog.V(2).Info("Error checking if mountpoint ", dir, ": ", err)
		return err
	}
	if notMnt {
		glog.V(2).Info("Not mountpoint, deleting")
		return os.Remove(dir)
	}

	refs, err := mount.GetMountRefs(c.mounter, dir)
	if err != nil {
		glog.V(2).Info("Error getting mountrefs for ", dir, ": ", err)
		return err
	}
	if len(refs) == 0 {
		glog.Warning("Did not find pod-mount for ", dir, " during tear-down")
	}
	// Unmount the bind-mount inside this pod
	if err := c.mounter.Unmount(dir); err != nil {
		glog.V(2).Info("Error unmounting dir ", dir, ": ", err)
		return err
	}
	// If len(refs) is 1, then all bind mounts have been removed, and the
	// remaining reference is the global mount. It is safe to detach.
	if len(refs) == 1 {
		// c.volumeID is not initially set for volume-unmounters, so set it here.
		c.volumeID, err = getVolumeIDFromGlobalMount(c.plugin.host, refs[0])
		if err != nil {
			glog.V(2).Info("Could not determine volumeID from mountpoint ", refs[0], ": ", err)
			return err
		}
		if err := c.manager.DetachDisk(&AzureDiskUnmounter{c.AzureDisk}); err != nil {
			glog.V(2).Info("Error detaching disk ", c.volumeID, ": ", err)
			return err
		}
	} else {
		glog.V(2).Infof("Found multiple refs; won't detach azure volume: %v", refs)
	}
	notMnt, mntErr := c.mounter.IsLikelyNotMountPoint(dir)
	if mntErr != nil {
		glog.Errorf("IsLikelyNotMountPoint check failed: %v", mntErr)
		return err
	}
	if notMnt {
		if err := os.Remove(dir); err != nil {
			glog.V(2).Info("Error removing mountpoint ", dir, ": ", err)
			return err
		}
	}
	return nil
}
