## Glusterfs

[Glusterfs](http://www.gluster.org) is an open source scale-out filesystem. These examples provide information about how to allow Docker containers use Glusterfs volumes.

The examples consist of:
- A pod that runs on hosts that install Glusterfs client package.
- A pod that runs on hosts that don't install Glusterfs client package and use [Super Privileged Container](http://developerblog.redhat.com/2014/11/06/introducing-a-super-privileged-container-concept/) to mount Glusterfs.

### Prerequisites

Either install Glusterfs client package on the hosts or run a Super Privileged Containers that [install Glusterfs client package](https://huaminchen.wordpress.com/2015/03/05/9/).

### Create a POD

The following *volume* spec illustrates a sample configuration.

```js
{
     "name": "glusterfsvol",
     "glusterfs": {
        "hosts": [
            "10.16.154.81",
            "10.16.154.82",
            "10.16.154.83"
        ],
        "path": "kube_vol",
        "mountOptions": "",
        "helper": "nsenter --mount=/proc/`docker inspect --format {{.State.Pid}} gluster_spc`/ns/mnt"
    }
}
```

The parameters are explained as the followings. **hosts** is an array of Gluster hosts. **kubelet** is optimized to avoid mount storm, it will randomly pick one from the hosts to mount. If this host is unresponsive, the next host in the array is automatically selected. **path** is the Glusterfs volume name. **mountOption** is the mount time options. **helper** is used if mount is executed inside a Super Privileged Container. This **helper** assumes the name of the Super Proviliged Container is *gluster_spc*.

Detailed POD information can be found at [v1beta3/](v1beta3/)

```shell
$ kubectl create -f examples/glusterfs/v1beta3/glusterfs.json
```
Once that's up you can list the pods in the cluster, to verify that the master is running:

```shell
$ kubectl get pods
```

If you ssh to that machine, you can run `docker ps` to see the actual pod and `mount` to see if the Glusterfs volume is mounted.