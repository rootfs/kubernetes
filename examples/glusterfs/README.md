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
        "endpoints": "glusterfs-cluster",
        "path": "kube_vol",
        "readOnly": true,
        "helper": ""
    }
}
```

The parameters are explained as the followings. **endpoints** is endpoint name that defines Gluster service. **kubelet** is optimized to avoid mount storm, it will randomly pick one from the hosts to mount. If this host is unresponsive, the next host in the array is automatically selected. **path** is the Glusterfs volume name. **readOnly** is the boolean that sets the mountpoint readOnly or readWrite. **helper** can be a command that can be executed prior to mounting the filesystem.

Detailed POD and Gluster cluster endpoints examples can be found at [v1beta3/](v1beta3/) and [endpoints/](endpoints/)

```shell
$ kubectl create -f examples/glusterfs/endpoints/glusterfs-endpoints.json
$ kubectl create -f examples/glusterfs/v1beta3/glusterfs.json
```
Once that's up you can list the pods and endpoint in the cluster, to verify that the master is running:

```shell
$ kubectl get endpoints
$ kubectl get pods
```

If you ssh to that machine, you can run `docker ps` to see the actual pod and `mount` to see if the Glusterfs volume is mounted.