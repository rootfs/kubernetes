// +build linux

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

package mount

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/golang/glog"
)

type DSMounter struct {
	port         string
	cmdPath      string
	hostBindPath string
}

// NewDSMounter creates RESTful client
func NewDSMounter(p string, path, h string) *DSMounter {
	return &DSMounter{
		port:         p,
		cmdPath:      path,
		hostBindPath: h,
	}
}

// DSMounter implements mount.Interface
var _ = Interface(&DSMounter{})

func (ds *DSMounter) Mount(source string, target string, fstype string, options []string) error {
	cmd := fmt.Sprintf("-t %s %s %s/%s", fstype, source, ds.hostBindPath, target)
	if len(options) > 0 {
		cmd += fmt.Sprintf(" -o %s", strings.Join(options[:], ","))
	}
	encCmd := base64.URLEncoding.EncodeToString([]byte(cmd))
	url := fmt.Sprintf("http://127.0.0.1:%s%s%s", ds.port, ds.cmdPath, encCmd)
	res, err := http.Get(url)
	glog.V(1).Infof("mounting cmd: %s, url: %s", cmd, url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		output, err := ioutil.ReadAll(res.Body)
		if err != nil {
			return fmt.Errorf("failed to mount: %v", err)
		}
		return fmt.Errorf("failed to mount: %s", output)
	}
	return nil
}

// Unmount runs umount(8) in the host's mount namespace.
func (ds *DSMounter) Unmount(target string) error {
	glog.V(5).Infof("Unmounting %s", target)
	command := exec.Command("umount", target)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Unmount failed: %v\nUnmounting arguments: %s\nOutput: %s\n", err, target, string(output))
	}
	return nil
}

// List returns a list of all mounted filesystems in the host's mount namespace.
func (ds *DSMounter) List() ([]MountPoint, error) {
	return listProcMounts(hostProcMountsPath)
}

// IsLikelyNotMountPoint determines whether a path is a mountpoint by calling findmnt
// in the host's root mount namespace.
func (ds *DSMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	stat, err := os.Stat(file)
	if err != nil {
		return true, err
	}
	rootStat, err := os.Lstat(file + "/..")
	if err != nil {
		return true, err
	}
	// If the directory has a different device as parent, then it is a mountpoint.
	if stat.Sys().(*syscall.Stat_t).Dev != rootStat.Sys().(*syscall.Stat_t).Dev {
		return false, nil
	}

	return true, nil

}
