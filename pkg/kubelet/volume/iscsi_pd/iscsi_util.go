package iscsi_pd

import (
	"errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/volume"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubelet/volume/gce_pd"
	"github.com/golang/glog"
	"os"
	"path"
	"strings"
	"time"
)

func makeGlobalPDName(host volume.Host, portal string, iqn string, lun string) string {
	return path.Join(host.GetPluginDir(ISCSIDiskPluginName), "iscsi", portal, iqn, "lun", lun)
}

func probeDevicePath(devicePath string, maxRetries int) bool {
	numTries := 0
	for {
		_, err := os.Stat(devicePath)
		if err == nil {
			return true
		}
		if err != nil && !os.IsNotExist(err) {
			return false
		}
		numTries++
		if numTries == maxRetries {
			break
		}
		time.Sleep(time.Second)
	}
	return false
}

type ISCSIDiskUtil struct{}

func (util *ISCSIDiskUtil) AttachDisk(iscsi *iscsiDisk) error {
	devicePath := strings.Join([]string{"/dev/disk/by-path/ip", iscsi.portal, "iscsi", iscsi.iqn, "lun", iscsi.lun}, "-")
	exist := probeDevicePath(devicePath, 1)
	if exist == false {
		// login to iscsi target
		output, err := iscsi.execCommand("iscsiadm", []string{"-m", "node", "-p",
			iscsi.portal, "-T", iscsi.iqn, "--login"})
		if err != nil {
			glog.Infof("iscsiPersistentDisk: failed to attach disk:%s", output)
			return err
		}
		exist = probeDevicePath(devicePath, 10)
		if !exist {
			return errors.New("Could not attach disk: Timeout after 10s")
		}
	}
	// mount it
	globalPDPath := makeGlobalPDName(iscsi.plugin.host, iscsi.portal, iscsi.iqn, iscsi.lun)
	mountpoint, err := gce_pd.IsMountPoint(globalPDPath)
	if mountpoint {
		glog.Infof("iscsiPersistentDisk: %s already mounted", globalPDPath)
		return nil
	}

	if err := os.MkdirAll(globalPDPath, 0750); err != nil {
		glog.Infof("iSCSIPersistentDisk: failed to mkdir %s, error", globalPDPath)
		return err
	}

	err = iscsi.mounter.Mount(devicePath, globalPDPath, iscsi.fsType, uintptr(0), "")
	if err != nil {
		glog.Infof("iSCSIPersistentDisk: failed to mount iscsi volume %s [%s] to %s, error %v",
			devicePath, iscsi.fsType, globalPDPath, err)
	}

	return err
}

func (util *ISCSIDiskUtil) DetachDisk(iscsi *iscsiDisk, devicePath string) error {
	globalPDPath := makeGlobalPDName(iscsi.plugin.host, iscsi.portal, iscsi.iqn, iscsi.lun)
	if err := iscsi.mounter.Unmount(globalPDPath, 0); err != nil {
		glog.Infof("iSCSIPersistentDisk: failed to umount: %s\nError: %v", globalPDPath, err)
		return err
	}

	output, err := iscsi.execCommand("iscsiadm", []string{"-m", "node", "-p",
		iscsi.portal, "-T", iscsi.iqn, "--logout"})
	if err != nil {
		glog.Infof("iSCSIPersistentDisk: failed to detach disk Output: %s\nError: %v", output, err)
	}
	return err
}
