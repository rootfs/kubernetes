# iSCSI Example Pod 
The ISCSI example pod demonstrates read/write access to a volume backed by an  iscsi target.  A couple of notes-

* Only one POD can mount the volume for write+read. Other pods may only access the volume readonly.

* The iSCSI target must contain a filesystem which is specified in the volume definition.

# How to test the iscsi POD example

* Setup and configure iSCSI per your platform.  This typically involves scsi target utilities.  I used this on Fedora: http://fedoraproject.org/wiki/Scsi-target-utils_Quickstart_Guide
* If using ```tgtadm```, list all active targets:

`tgtadm --lld iscsi --mode target --op show`

- If this doesn't return anything then your iscsi target isn't setup propertly.

# Configure a pod to access iSCSI target:
Configure the volume definition such as:

```

              "volumes": [
                {
                    "name": "iscsipd-ro",
                    "source": {
                        "iscsiDisk": {
                            "portal": "10.16.154.81:3260",
                            "iqn": "iqn.2014-12.world.server:storage.target00",
                            "lun": 0,
                            "fsType": "ext4",
                            "readOnly": true
                        }
                    }
                },
```

* replace **portal**, **IQN** and **LUN** with appropriate values for your target device
