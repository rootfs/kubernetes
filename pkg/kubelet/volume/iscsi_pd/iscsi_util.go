package iscsi_pd

import (
	"github.com/golang/glog"
)

type ISCSIDiskUtil struct{}

func (util *ISCSIDiskUtil) AttachDisk(iscsi *iscsiDisk) error {
	output, err := iscsi.execCommand("iscsiadm", []string{"-m", "node", "-T",
		iscsi.portal, "-p", iscsi.iqn})
	glog.V(4).Infof("AttachDisk: Output: %s\nError: %v", output, err)
	return err
}

func (util *ISCSIDiskUtil) DetachDisk(iscsi *iscsiDisk, path string) error {
	return nil
}
