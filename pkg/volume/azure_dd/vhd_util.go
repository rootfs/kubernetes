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
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/arm/compute"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/util/exec"
)

type ioHandler interface {
	ReadDir(dirname string) ([]os.FileInfo, error)
	ReadFile(filename string) ([]byte, error)
}

type osIOHandler struct{}

func (handler *osIOHandler) ReadDir(dirname string) ([]os.FileInfo, error) {
	return ioutil.ReadDir(dirname)
}
func (handler *osIOHandler) ReadFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filename)
}

// given a LUN find the VHD device path like /dev/sdb
// VHD disks under sysfs are like /sys/bus/scsi/devices/3:0:1:0
func findDiskByLun(lun int, io ioHandler) string {
	sys_path := "/sys/bus/scsi/devices"
	if dirs, err := io.ReadDir(sys_path); err == nil {
		for _, f := range dirs {
			name := f.Name()
			// look for path like /sys/bus/scsi/devices/3:0:1:0
			arr := strings.Split(name, ":")
			if len(arr) < 4 {
				continue
			}
			target, err := strconv.Atoi(arr[0])
			// skip targets 0-2, which are used by OS disks
			if err == nil && target > 2 {
				l, err := strconv.Atoi(arr[2])
				if err == nil && lun == l {
					// find the matching LUN
					// read vendor and model to ensure it is a VHD disk
					vendor := path.Join(sys_path, name, "vendor")
					model := path.Join(sys_path, name, "model")
					exe := exec.New()
					out, err := exe.Command("cat", vendor, model).CombinedOutput()
					if err != nil {
						continue
					}
					matched, err := regexp.MatchString("^MSFT[ ]{0,}\nVIRTUAL DISK[ ]{0,}\n$", strings.ToUpper(string(out)))
					if err != nil || !matched {
						continue
					}
					// find it!
					if dev, err := io.ReadDir(path.Join(sys_path, name, "block")); err == nil {
						return "/dev/" + dev[0].Name()
					}
				}
			}
		}
	}
	return ""
}

type azureDiskUtil struct{}

func (util *azureDiskUtil) AttachDisk(b *azureDiskMounter, vmName string) error {
	m, err := b.util.getAzureSecret(b.plugin.host, b.namespace, b.secretName)
	if err != nil {
		glog.Errorf("failed to get Azure secret and config")
		return err
	}

	var op azureDataDiskOp
	op.action = ATTACH
	op.name = b.diskName
	op.uri = b.diskUri
	op.caching = compute.CachingTypes(b.cachingMode)
	err = b.util.vmDataDisksOp(m, op, vmName)
	if err != nil {
		glog.Errorf("failed to attach disk %q to host %q", b.diskName, vmName)
		return err
	}
	return nil
}

func (util *azureDiskUtil) DetachDisk(b *azureDiskUnmounter, vmName string) error {
	m, err := b.util.getAzureSecret(b.plugin.host, b.namespace, b.secretName)
	if err != nil {
		glog.Errorf("failed to get Azure secret and config")
		return err
	}

	var op azureDataDiskOp
	op.action = DETACH
	op.lun = b.lun
	err = b.util.vmDataDisksOp(m, op, vmName)
	if err != nil {
		glog.Errorf("failed to detach disk (lun=%q) to host %q", b.lun, vmName)
		return err
	}
	return nil
}
