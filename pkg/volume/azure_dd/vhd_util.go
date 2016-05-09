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
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/volume"
)

type ioHandler interface {
	ReadDir(dirname string) ([]os.FileInfo, error)
	ReadFile(filename string)([]byte, error)
}

type osIOHandler struct{}

func (handler *osIOHandler) ReadDir(dirname string) ([]os.FileInfo, error) {
	return ioutil.ReadDir(dirname)
}
func (handler *osIOHandler) ReadFile(filename string)([]byte, error) {
	return ioutil.ReadFile(filename)
}

// given a LUN find the VHD device path like /dev/sdb
func findDiskByLun(lun int, io ioHandler) string {
	sys_path := "/sys/bus/scsi/devices"
	if dirs, err := io.ReadDir(sys_path); err == nil {
		for _, f := range dirs {
			name := f.Name()
			arr := strings.Split(name,":")
			if len(arr) < 4 {
				continue
			}
			target,err := strconv.Atoi(arr[0])
			// skip targets 0-2, which are used by OS disks
			if err == nil && target > 2  {
				l,err := strconv.Atoi(arr[2])
				if err == nil && lun == l {
					// read vendor
					if vendor, err := io.ReadFile(path.Join(sys_path, name,"vendor")); err == nil {
						if strings.ToUpper(string(vendor)) == "MSFT" {
							// read model
							if model, err := ioutil.ReadFile(path.Join(sys_path, name,"model")); err == nil {
								if strings.ToUpper(string(vendor)) == "VIRTUAL DISK" {
									// found it
									if dev, err := io.ReadDir(path.Join(sys_path, name, "block")); err == nil {
										return "/dev/" + dev[0].Name()
									}
								}
							}
						}
					}				
				}
			}
		}
	}
	return ""
}



func makePDNameInternal(host volume.VolumeHost, lun string) string {
	return path.Join(host.GetPluginDir(azurePluginName), "lun-"+lun)
}

type FCUtil struct{}

func (util *FCUtil) MakeGlobalPDName(fc fcDisk) string {
	return makePDNameInternal(fc.plugin.host, fc.wwns, fc.lun)
}

func (util *FCUtil) AttachDisk(b azureDiskMounter) error {
	lun := b.lun
	io := b.io
	disk := findDiskByLun(lun, io)
	// if no disk matches input lun, exit
	if disk == "" {
		return fmt.Errorf("no vhd disk found")
	}
	if b.parition != "" {
		disk = disk + b.partition
	}
	// mount it
	globalPDPath := b.manager.MakeGlobalPDName(*b.fcDisk)
	noMnt, err := b.mounter.IsLikelyNotMountPoint(globalPDPath)
	if !noMnt {
		glog.Infof("azure disk: %s already mounted", globalPDPath)
		return nil
	}

	if err := os.MkdirAll(globalPDPath, 0750); err != nil {
		return fmt.Errorf("azure disk: failed to mkdir %s, error", globalPDPath)
	}

	err = b.mounter.FormatAndMount(disk, globalPDPath, b.fsType, nil)
	if err != nil {
		return fmt.Errorf("azure disk: failed to mount volume %s [%s] to %s, error %v", devicePath, b.fsType, globalPDPath, err)
	}

	return err
}

func (util *FCUtil) DetachDisk(c fcDiskUnmounter, mntPath string) error {
	if err := c.mounter.Unmount(mntPath); err != nil {
		return fmt.Errorf("azure disk: detach disk: failed to unmount: %s\nError: %v", mntPath, err)
	}
	return nil
}
