/*
Copyright 2015 Google Inc. All rights reserved.

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

package glusterfs

import (
	"math/rand"
	"os"
	"strings"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/types"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/exec"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util/mount"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/volume"
	"github.com/golang/glog"
)

// This is the primary entrypoint for volume plugins.
func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&glusterfsPlugin{nil}}
}

type glusterfsPlugin struct {
	host volume.VolumeHost
}

var _ volume.VolumePlugin = &glusterfsPlugin{}

const (
	glusterfsPluginName = "kubernetes.io/glusterfs"
)

func (plugin *glusterfsPlugin) Init(host volume.VolumeHost) {
	plugin.host = host
}

func (plugin *glusterfsPlugin) Name() string {
	return glusterfsPluginName
}

func (plugin *glusterfsPlugin) CanSupport(spec *api.Volume) bool {
	if spec.VolumeSource.Glusterfs != nil {
		return true
	}
	return false
}

func (plugin *glusterfsPlugin) GetAccessModes() []api.AccessModeType {
	return []api.AccessModeType{
		api.ReadWriteOnce,
		api.ReadOnlyMany,
		api.ReadWriteMany,
	}
}

func (plugin *glusterfsPlugin) NewBuilder(spec *api.Volume, podRef *api.ObjectReference) (volume.Builder, error) {
	ep_name := spec.VolumeSource.Glusterfs.EndpointsName
	ns := api.NamespaceDefault
	ep, err := plugin.host.GetKubeClient().Endpoints(ns).Get(ep_name)
	if err != nil {
		glog.Errorf("Glusterfs: failed to get endpoints %s[%v]", ep_name, err)
		return nil, err
	}
	glog.Infof("Glusterfs: endpoints %v", ep)
	return plugin.newBuilderInternal(spec, ep, podRef, mount.New(), exec.New())
}

func (plugin *glusterfsPlugin) newBuilderInternal(spec *api.Volume, ep *api.Endpoints, podRef *api.ObjectReference, mounter mount.Interface, exe exec.Interface) (volume.Builder, error) {
	return &glusterfs{
		volName:  spec.Name,
		hosts:    ep,
		path:     spec.VolumeSource.Glusterfs.Path,
		readonly: spec.VolumeSource.Glusterfs.ReadOnly,
		helper:   spec.VolumeSource.Glusterfs.Helper,
		mounter:  mounter,
		exe:      exe,
		podRef:   podRef,
		plugin:   plugin,
	}, nil
}

func (plugin *glusterfsPlugin) NewCleaner(volName string, podUID types.UID) (volume.Cleaner, error) {
	return plugin.newCleanerInternal(volName, podUID, mount.New())
}

func (plugin *glusterfsPlugin) newCleanerInternal(volName string, podUID types.UID, mounter mount.Interface) (volume.Cleaner, error) {
	return &glusterfs{
		volName: volName,
		mounter: mounter,
		podRef:  &api.ObjectReference{UID: podUID},
		plugin:  plugin,
	}, nil
}

// Glusterfs volumes represent a bare host file or directory mount of an Glusterfs export.
type glusterfs struct {
	volName  string
	podRef   *api.ObjectReference
	hosts    *api.Endpoints
	path     string
	readonly bool
	helper   string
	mounter  mount.Interface
	exe      exec.Interface
	plugin   *glusterfsPlugin
}

// SetUp attaches the disk and bind mounts to the volume path.
func (glusterfsVolume *glusterfs) SetUp() error {
	return glusterfsVolume.SetUpAt(glusterfsVolume.GetPath())
}

func (glusterfsVolume *glusterfs) SetUpAt(dir string) error {
	mountpoint, err := mount.IsMountPoint(dir)
	glog.V(4).Infof("Glusterfs: mount set up: %s %v %v", dir, mountpoint, err)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if mountpoint {
		return nil
	}
	path := glusterfsVolume.path
	os.MkdirAll(dir, 0750)
	err = glusterfsVolume.execMount(glusterfsVolume.hosts, path, dir, glusterfsVolume.readonly, glusterfsVolume.helper)
	if err == nil {
		return nil
	}

	// cleanup upon failure
	glusterfsVolume.cleanup(dir)
	// return error
	return err
}

func (glusterfsVolume *glusterfs) GetPath() string {
	name := glusterfsPluginName
	return glusterfsVolume.plugin.host.GetPodVolumeDir(glusterfsVolume.podRef.UID, util.EscapeQualifiedNameForDisk(name), glusterfsVolume.volName)
}

func (glusterfsVolume *glusterfs) TearDown() error {
	return glusterfsVolume.TearDownAt(glusterfsVolume.GetPath())
}

func (glusterfsVolume *glusterfs) TearDownAt(dir string) error {
	return glusterfsVolume.cleanup(dir)
}

func (glusterfsVolume *glusterfs) cleanup(dir string) error {
	mountpoint, err := mount.IsMountPoint(dir)
	if err != nil {
		glog.Errorf("Glusterfs: Error checking IsMountPoint: %v", err)
		return err
	}
	if !mountpoint {
		return os.RemoveAll(dir)
	}

	if err := glusterfsVolume.mounter.Unmount(dir, 0); err != nil {
		glog.Errorf("Glusterfs: Unmounting failed: %v", err)
		return err
	}
	mountpoint, mntErr := mount.IsMountPoint(dir)
	if mntErr != nil {
		glog.Errorf("Glusterfs: IsMountpoint check failed: %v", mntErr)
		return mntErr
	}
	if !mountpoint {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}

	return nil
}

func (glusterfsVolume *glusterfs) execMount(hosts *api.Endpoints, path string, mountpoint string, readonly bool, helper string) error {
	var errs error
	var command exec.Cmd
	var mountArgs []string
	var opt []string

	// build option array
	if readonly == true {
		opt = []string{"-o", "ro"}
	} else {
		opt = []string{"-o", "rw"}
	}

	l := len(hosts.Subsets[0].Addresses)
	// avoid mount storm, pick a host randomly
	start := rand.Int() % l
	// iterate all hosts until mount succeeds.
	for i := start; i < start+l; i++ {
		if helper == "" {
			arg := []string{"-t", "glusterfs", hosts.Subsets[0].Addresses[i%l].IP + ":" + path, mountpoint}
			mountArgs = append(arg, opt...)
			glog.Infof("Glusterfs: mount cmd: mount %v", strings.Join(mountArgs, " "))
			command = glusterfsVolume.exe.Command("mount", mountArgs...)
		} else {
			// if helper is provided, make a cmd like "helper_cmd helper_arg mount -t glusterfs mnt -o option"
			helper_array := strings.Split(helper, " ")
			arg := []string{"mount", "-t", "glusterfs", hosts.Subsets[0].Addresses[i%l].IP + ":" + path, mountpoint}
			mountArgs = append(arg, opt...)
			args := append(helper_array[1:], mountArgs...)
			glog.Infof("Glusterfs: mount cmd: %s %v", helper_array[0], strings.Join(args, " "))
			command = glusterfsVolume.exe.Command(helper_array[0], args...)
		}

		_, errs = command.CombinedOutput()
		if errs == nil {
			return nil
		}
	}
	glog.Errorf("Glusterfs: mount failed: %v", errs)
	return errs
}
