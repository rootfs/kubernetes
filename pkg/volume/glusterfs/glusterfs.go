/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util"
	"k8s.io/kubernetes/pkg/util/exec"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
)

// This is the primary entrypoint for volume plugins.
func ProbeVolumePlugins() []volume.VolumePlugin {
	return []volume.VolumePlugin{&glusterfsPlugin{nil}}
}

type glusterfsPlugin struct {
	host volume.VolumeHost
}

var _ volume.VolumePlugin = &glusterfsPlugin{}
var _ volume.PersistentVolumePlugin = &glusterfsPlugin{}

const (
	glusterfsPluginName = "kubernetes.io/glusterfs"
)

func (plugin *glusterfsPlugin) Init(host volume.VolumeHost) {
	plugin.host = host
}

func (plugin *glusterfsPlugin) Name() string {
	return glusterfsPluginName
}

func (plugin *glusterfsPlugin) CanSupport(spec *volume.Spec) bool {
	return (spec.PersistentVolume != nil && spec.PersistentVolume.Spec.Glusterfs != nil) ||
		(spec.Volume != nil && spec.Volume.Glusterfs != nil)
}

func (plugin *glusterfsPlugin) GetAccessModes() []api.PersistentVolumeAccessMode {
	return []api.PersistentVolumeAccessMode{
		api.ReadWriteOnce,
		api.ReadOnlyMany,
		api.ReadWriteMany,
	}
}

func (plugin *glusterfsPlugin) NewBuilder(spec *volume.Spec, pod *api.Pod, _ volume.VolumeOptions, mounter mount.Interface) (volume.Builder, error) {
	source, _ := plugin.getGlusterVolumeSource(spec)
	ep_name := source.EndpointsName
	ns := pod.Namespace
	ep, err := plugin.host.GetKubeClient().Endpoints(ns).Get(ep_name)
	if err != nil {
		glog.Errorf("Glusterfs: failed to get endpoints %s[%v]", ep_name, err)
		return nil, err
	}
	glog.V(1).Infof("Glusterfs: endpoints %v", ep)
	return plugin.newBuilderInternal(spec, ep, pod, mounter, exec.New())
}

func (plugin *glusterfsPlugin) getGlusterVolumeSource(spec *volume.Spec) (*api.GlusterfsVolumeSource, bool) {
	// Glusterfs volumes used directly in a pod have a ReadOnly flag set by the pod author.
	// Glusterfs volumes used as a PersistentVolume gets the ReadOnly flag indirectly through the persistent-claim volume used to mount the PV
	if spec.Volume != nil && spec.Volume.Glusterfs != nil {
		return spec.Volume.Glusterfs, spec.Volume.Glusterfs.ReadOnly
	} else {
		return spec.PersistentVolume.Spec.Glusterfs, spec.ReadOnly
	}
}

func (plugin *glusterfsPlugin) newBuilderInternal(spec *volume.Spec, ep *api.Endpoints, pod *api.Pod, mounter mount.Interface, exe exec.Interface) (volume.Builder, error) {
	source, readOnly := plugin.getGlusterVolumeSource(spec)
	return &glusterfsBuilder{
		glusterfs: &glusterfs{
			volName: spec.Name(),
			mounter: mounter,
			pod:     pod,
			plugin:  plugin,
		},
		hosts:    ep,
		path:     source.Path,
		readOnly: readOnly,
		exe:      exe}, nil
}

func (plugin *glusterfsPlugin) NewCleaner(volName string, podUID types.UID, mounter mount.Interface) (volume.Cleaner, error) {
	return plugin.newCleanerInternal(volName, podUID, mounter)
}

func (plugin *glusterfsPlugin) newCleanerInternal(volName string, podUID types.UID, mounter mount.Interface) (volume.Cleaner, error) {
	return &glusterfsCleaner{&glusterfs{
		volName: volName,
		mounter: mounter,
		pod:     &api.Pod{ObjectMeta: api.ObjectMeta{UID: podUID}},
		plugin:  plugin,
	}}, nil
}

// Glusterfs volumes represent a bare host file or directory mount of an Glusterfs export.
type glusterfs struct {
	volName string
	pod     *api.Pod
	mounter mount.Interface
	plugin  *glusterfsPlugin
}

type glusterfsBuilder struct {
	*glusterfs
	hosts    *api.Endpoints
	path     string
	readOnly bool
	exe      exec.Interface
}

var _ volume.Builder = &glusterfsBuilder{}

// SetUp attaches the disk and bind mounts to the volume path.
func (b *glusterfsBuilder) SetUp() error {
	return b.SetUpAt(b.GetPath())
}

func (b *glusterfsBuilder) SetUpAt(dir string) error {
	notMnt, err := b.mounter.IsLikelyNotMountPoint(dir)
	glog.V(4).Infof("Glusterfs: mount set up: %s %v %v", dir, !notMnt, err)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !notMnt {
		return nil
	}

	os.MkdirAll(dir, 0750)
	err = b.setUpAtInternal(dir)
	if err == nil {
		return nil
	}

	// Cleanup upon failure.
	c := &glusterfsCleaner{b.glusterfs}
	c.cleanup(dir)
	return err
}

func (b *glusterfsBuilder) IsReadOnly() bool {
	return b.readOnly
}

func (glusterfsVolume *glusterfs) GetPath() string {
	name := glusterfsPluginName
	return glusterfsVolume.plugin.host.GetPodVolumeDir(glusterfsVolume.pod.UID, util.EscapeQualifiedNameForDisk(name), glusterfsVolume.volName)
}

type glusterfsCleaner struct {
	*glusterfs
}

var _ volume.Cleaner = &glusterfsCleaner{}

func (c *glusterfsCleaner) TearDown() error {
	return c.TearDownAt(c.GetPath())
}

func (c *glusterfsCleaner) TearDownAt(dir string) error {
	return c.cleanup(dir)
}

func (c *glusterfsCleaner) cleanup(dir string) error {
	notMnt, err := c.mounter.IsLikelyNotMountPoint(dir)
	if err != nil {
		glog.Errorf("Glusterfs: Error checking IsLikelyNotMountPoint: %v", err)
		return err
	}
	if notMnt {
		var pod *api.Pod
		pod = nil
		errs := c.plugin.loadPod(pod, dir)
		if errs == nil {
			c.plugin.deletePod(pod.Namespace, pod.Name)
			c.plugin.removePod(dir)
		}
		return os.RemoveAll(dir)
	}

	if err := c.mounter.Unmount(dir); err != nil {
		glog.Errorf("Glusterfs: Unmounting failed: %v", err)
		return err
	}
	notMnt, mntErr := c.mounter.IsLikelyNotMountPoint(dir)
	if mntErr != nil {
		glog.Errorf("Glusterfs: IsLikelyNotMountPoint check failed: %v", mntErr)
		return mntErr
	}
	if notMnt {
		var pod *api.Pod
		pod = nil
		errs := c.plugin.loadPod(pod, dir)
		if errs == nil {
			c.plugin.deletePod(pod.Namespace, pod.Name)
			c.plugin.removePod(dir)
		}

		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}

	return nil
}

func (b *glusterfsBuilder) setUpAtInternal(dir string) error {
	var errs error

	options := []string{}
	if b.readOnly {
		options = append(options, "ro")
	}

	l := len(b.hosts.Subsets)
	// Avoid mount storm, pick a host randomly.
	start := rand.Int() % l
	// Iterate all hosts until mount succeeds.
	for i := start; i < start+l; i++ {
		hostIP := b.hosts.Subsets[i%l].Addresses[0].IP
		errs = b.mounter.Mount(hostIP+":"+b.path, dir, "glusterfs", options)
		if errs == nil {
			return nil
		}
	}
	// try containerized mount
	//FIXME: container image hard coded to fs_client
	pod, err := b.plugin.createSidecarContainer("fs_client")
	if pod == nil {
		glog.Errorf("Glusterfs: failed to create mounter pod: %v", err)
		return errs
	}
	// persist pod
	b.plugin.persistPod(pod, dir)

	for i := start; i < start+l; i++ {
		hostIP := b.hosts.Subsets[i%l].Addresses[0].IP
		cmd := []string{"mount", "-t", "glusterfs", hostIP + ":" + b.path, "/host/" + dir}
		out, err := b.plugin.runInSidecarContainer(pod, cmd)
		glog.Infof("Glusterfs: container mount output %s: err:%v", out, err)
		notMnt, mntErr := b.mounter.IsLikelyNotMountPoint(dir)
		if mntErr == nil && !notMnt {
			glog.Infof("Glusterfs: container mount output succeeded")
			return nil
		}
	}
	b.plugin.deletePod(pod.Namespace, pod.Name)
	b.plugin.removePod(dir)
	glog.Errorf("Glusterfs: mount failed: %v", errs)
	return errs
}

func (plugin *glusterfsPlugin) createSidecarContainer(containerName string) (*api.Pod, error) {
	priv := true
	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			GenerateName: "glusterfs-mounter-",
			Namespace:    api.NamespaceDefault,
		},
		Spec: api.PodSpec{
			RestartPolicy: api.RestartPolicyAlways,
			//HostNetwork:   true,
			Volumes: []api.Volume{
				{
					Name: "host",
					VolumeSource: api.VolumeSource{
						HostPath: &api.HostPathVolumeSource{
							Path: "/",
						},
					},
				},
				{
					Name: "var",
					VolumeSource: api.VolumeSource{
						HostPath: &api.HostPathVolumeSource{
							Path: "/var",
						},
					},
				},
				{
					Name: "dev",
					VolumeSource: api.VolumeSource{
						HostPath: &api.HostPathVolumeSource{
							Path: "/dev",
						},
					},
				},
			},
			Containers: []api.Container{
				{
					Name:  "glusterfs-mounter",
					Image: containerName,
					SecurityContext: &api.SecurityContext{
						Privileged: &priv,
					},
					Command: []string{"sleep"},
					Args:    []string{"infinity"},
					Stdout:  true,
					Stderr:  true,
					VolumeMounts: []api.VolumeMount{
						{
							Name:      "var",
							MountPath: "/var",
						},
						{
							Name:      "dev",
							MountPath: "/dev",
						},
						{
							Name:      "host",
							MountPath: "/host",
						},
					},
					RootMount: "container_shared",
				},
			},
		},
	}
	kubeClient := plugin.host.GetKubeClient()
	pod, err := kubeClient.Pods(pod.Namespace).Create(pod)
	if err != nil {
		return nil, fmt.Errorf("Failed to create pod %s:  %+v", pod.Name, err)
	}

	return pod, nil
}

func (plugin *glusterfsPlugin) runInSidecarContainer(pod *api.Pod, cmd []string) ([]byte, error) {
	container := &pod.Spec.Containers[0]
	return plugin.host.RunContainerCommand(pod, container, cmd, true)
}

func (plugin *glusterfsPlugin) deletePod(namespace, name string) {
	kubeClient := plugin.host.GetKubeClient()
	kubeClient.Pods(namespace).Delete(name, nil)
}

func (plugin *glusterfsPlugin) persistPod(pod *api.Pod, mnt string) error {
	file := path.Join(mnt, "pod.json")
	fp, err := os.Create(file)
	if err != nil {
		return fmt.Errorf("Glusterfs: create err %s/%s", file, err)
	}
	defer fp.Close()

	encoder := json.NewEncoder(fp)
	if err = encoder.Encode(pod); err != nil {
		return fmt.Errorf("Glusterfs: encode err: %v.", err)
	}

	return nil
}

func (plugin *glusterfsPlugin) loadPod(pod *api.Pod, mnt string) error {
	file := path.Join(mnt, "pod.json")
	fp, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("Glusterfs: open err %s/%s", file, err)
	}
	defer fp.Close()

	decoder := json.NewDecoder(fp)
	if err = decoder.Decode(pod); err != nil {
		return fmt.Errorf("Glusterfs: decode err: %v.", err)
	}

	return nil
}

func (plugin *glusterfsPlugin) removePod(mnt string) {
	file := path.Join(mnt, "pod.json")
	os.Remove(file)
}
