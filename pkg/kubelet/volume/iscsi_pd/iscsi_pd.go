/*
Copyright 2014 Google Inc. All rights reserved.

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

package iscsi_pd

import (
	"fmt"
	"os"
	"path"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/volume"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/volume/gce_pd"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/exec"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/mount"
	"github.com/golang/glog"
)

// This is the primary entrypoint for volume plugins.
func ProbeVolumePlugins() []volume.Plugin {
	return []volume.Plugin{&ISCSIDiskPlugin{nil, false}, &ISCSIDiskPlugin{nil, true}}
}

type ISCSIDiskPlugin struct {
	host       volume.Host
	legacyMode bool // if set, plugin answers to the legacy name
}

var _ volume.Plugin = &ISCSIDiskPlugin{}

const (
	ISCSIDiskPluginName       = "kubernetes.io/iscsi_pd"
	ISCSIDiskPluginLegacyName = "iscsi_pd"
)

// Abstract interface to PD operations.
type pdManager interface {
	// Attaches the disk to the kubelet's host machine.
	AttachDisk(iscsi *iscsiDisk) error
	// Detaches the disk from the kubelet's host machine.
	DetachDisk(iscsi *iscsiDisk, devicePath string) error
}

type iscsiDisk struct {
	volName    string
	podUID     types.UID
	portal     string
	iqn        string
	readOnly   bool
	plugin     *ISCSIDiskPlugin
	legacyMode bool
	mounter    mount.Interface
	// Utility interface that provides API calls to the provider to attach/detach disks.
	manager pdManager
	exec    exec.Interface
}

func (plugin *ISCSIDiskPlugin) Init(host volume.Host) {
	plugin.host = host
}

func (plugin *ISCSIDiskPlugin) Name() string {
	if plugin.legacyMode {
		return ISCSIDiskPluginLegacyName
	}
	return ISCSIDiskPluginName
}

func (plugin *ISCSIDiskPlugin) CanSupport(spec *api.Volume) bool {
	if plugin.legacyMode {
		// Legacy mode instances can be cleaned up but not created anew.
		return false
	}

	if spec.Source.ISCSIDisk != nil {
		return true
	}
	return false
}

func (plugin *ISCSIDiskPlugin) NewBuilder(spec *api.Volume, podUID types.UID) (volume.Builder, error) {
	if plugin.legacyMode {
		// Legacy mode instances can be cleaned up but not created anew.
		return nil, fmt.Errorf("legacy mode: can not create new instances")
	}
	return &iscsiDisk{
		podUID:     podUID,
		volName:    spec.Name,
		portal:     spec.Source.ISCSIDisk.Portal,
		iqn:        spec.Source.ISCSIDisk.IQN,
		readOnly:   spec.Source.ISCSIDisk.ReadOnly,
		exec:       exec.New(),
		manager:    &ISCSIDiskUtil{},
		mounter:    mount.New(),
		legacyMode: false,
	}, nil
}

func (iscsi *ISCSIDiskPlugin) NewCleaner(volName string, podUID types.UID) (volume.Cleaner, error) {
	// Inject real implementations here, test through the internal function.
	legacy := false
	if iscsi.legacyMode {
		legacy = true
	}
	return &iscsiDisk{
		podUID:     podUID,
		volName:    volName,
		legacyMode: legacy,
	}, nil
}

func (iscsi *iscsiDisk) GetPath() string {
	name := ISCSIDiskPluginName
	if iscsi.legacyMode {
		name = ISCSIDiskPluginLegacyName
	}
	return iscsi.plugin.host.GetPodVolumeDir(iscsi.podUID, volume.EscapePluginName(name), iscsi.volName)
}

func makeGlobalPDName(host volume.Host, portal string, iqn string, readOnly bool) string {
	flag := "rw"
	if readOnly {
		flag = "ro"
	}
	return path.Join(host.GetPluginDir(ISCSIDiskPluginName), "iscsi", portal, iqn, flag)
}

func (iscsi *iscsiDisk) SetUp() error {
	if iscsi.legacyMode {
		return fmt.Errorf("legacy mode: can not create new instances")
	}
	// TODO: handle failed mounts here.
	mountpoint, err := gce_pd.IsMountPoint(iscsi.GetPath())
	glog.V(4).Infof("iSCSIPersistentDisk set up: %s %v %v", iscsi.GetPath(), mountpoint, err)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if mountpoint {
		return nil
	}

	if err := iscsi.manager.AttachDisk(iscsi); err != nil {
		return err
	}

	flags := uintptr(0)
	if iscsi.readOnly {
		flags = mount.FlagReadOnly
	}

	volPath := iscsi.GetPath()
	if err := os.MkdirAll(volPath, 0750); err != nil {
		return err
	}

	// Perform a bind mount to the full path to allow duplicate mounts of the same PD.
	globalPDPath := makeGlobalPDName(iscsi.plugin.host, iscsi.portal, iscsi.iqn, iscsi.readOnly)
	err = iscsi.mounter.Mount(globalPDPath, iscsi.GetPath(), "", mount.FlagBind|flags, "")
	if err != nil {
		os.RemoveAll(iscsi.GetPath())
		return err
	}

	return nil

}

func (iscsi *iscsiDisk) TearDown() error {
	mountpoint, err := gce_pd.IsMountPoint(iscsi.GetPath())
	if err != nil {
		return err
	}
	if !mountpoint {
		return os.RemoveAll(iscsi.GetPath())
	}

	devicePath, refCount, err := gce_pd.GetMountRefCount(iscsi.mounter, iscsi.GetPath())
	if err != nil {
		return err
	}
	if err := iscsi.mounter.Unmount(iscsi.GetPath(), 0); err != nil {
		return err
	}
	refCount--
	if err := os.RemoveAll(iscsi.GetPath()); err != nil {
		return err
	}
	// If refCount is 1, then all bind mounts have been removed, and the
	// remaining reference is the global mount. It is safe to detach.
	if refCount == 1 {
		if err := iscsi.manager.DetachDisk(iscsi, devicePath); err != nil {
			return err
		}
	}
	return nil

}

func (iscsi *iscsiDisk) execCommand(command string, args []string) ([]byte, error) {
	cmd := iscsi.exec.Command(command, args...)
	return cmd.CombinedOutput()
}
