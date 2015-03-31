## Glusterfs

[Glusterfs](http://www.gluster.org) is an open source scale-out filesystem. These examples provide information about how to allow Docker containers use Glusterfs volumes.

The example consists of a pod that runs on hosts that install Glusterfs client package.

### Prerequisites

Install Glusterfs client package on the hosts.

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
        "mountOptions": "ro",
        "helper": ""
    }
}
```

The parameters are explained as the followings. **hosts** is an array of Gluster hosts. **kubelet** is optimized to avoid mount storm, it will randomly pick one from the hosts to mount. If this host is unresponsive, the next host in the array is automatically selected. **path** is the Glusterfs volume name. **mountOption** is the mount time options, it can cotains standard mount time options such as *ro* (readonly). **helper** can be a command that can be executed prior to mounting the filesystem.

Detailed POD information can be found at [v1beta3/](v1beta3/)

```shell
$ kubectl create -f examples/glusterfs/v1beta3/glusterfs.json
```
Once that's up you can list the pods in the cluster, to verify that the master is running:

```shell
$ kubectl get pods
```

If you ssh to that machine, you can run `docker ps` to see the actual pod and `mount` to see if the Glusterfs volume is mounted.