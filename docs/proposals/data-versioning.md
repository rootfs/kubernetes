# Data Volume Versioning
========================

## Abstract
Data Volume Versioning is like `git`: data volumes are cloned and branched off a shared root. Since each volume is separated, reading and writing are locally isolated. Containers are thus free to use the versioned volumes without data protection concerns. A versioned volume can later be the root of other volumes.

Data Volume Versioning provides self-service data management. It eliminates administrative overhead in providing data sharing, protection, and branching in shared environments: DevOps are able to validate their product using cloned production system; production data are tracked, retained, monitored, and even rolled-back.

Data Volume Versioning is based on Snapshot and Clone (also knowns as Writable Snapshot) technology. 

## Use Case Scenarios

### Cross-team Data Access
In a typical Web app organization, Development team produces products, Qualify Assurance team detects defects, and Sustaining team responds to customers' service calls. These teams usually examine the same data set through different angles. However, the master data, which could be production data, should be protected. As a result, it is not rare to find cases where teams have to request storage administrators to create a copy of the master database from a central repository and put the replica somewhere else before they can use the data.

The Data Volume Versioning service eliminates such overhead as illustrated below ![alt text][flowchart].  

When teams operate on the volume where the master database resides, they tell in the Pod to create a copy of the master data volume. Kubernetes then works with the Data Volume Providers to create clone of the master data volume. Then Kubelets mount the volume clone and present to the containers. 

[flowchart]: volume-versionining/volume-clone.png

### Data Provider and Consumers
Likewise, a Cloud based data provider offers data access through this architecture, as illustrated below ![alt text][data consumer]


Rather than making copies and sending them to each data consumer, the data provider can be instructed to clone from the source data into different data volumes and apply access control polices on each volume. Then each downstream data consumer owns a copy of data.

[data consumer]: volume-versionining/volume-provider-consumer.png

## Design Discussion
In a Pod definition, each `volume` is augumented with a new kind type to describe the volume hierarchy. 

In the following example, a Pod that uses a versioned volume is like the following 
```yaml
....
volumes:
    name: cloned_volume
    versionedVolume:
       versionName: dev_branch
```

Where `versionedVolume` refers to a resource defined as the following:
```yaml
kind: volumeVersioning
apiVersion: v1
metadata:
  name: dev_branch
spec:
  accessModes:
    - ReadWrite
  volume:
    glusterfs:
       endpoints: glusterfs-cluster,
       path: master_volume
```

This specification bases the new volume `dev_branch` on a Glusterfs volume called `master_volume`.

Upon receiving this Pod, Kubernetes starts the process to create a volume clone, passes clone information to Kubelet and mount the clone volume on the nodes. The process is demonstrated below ![alt text][process]

[process]: volume-versionining/create-data-volume-clone.png

It can be concluded that Data Volume Versioning shares many similarity with `Persistent Volume`: both apply some forms of transformation on the master volume. The derived volumes are consumed by the Pod. However, versioned volume are bound to a named volume (vs. picking from volume pools in Persistent Volume), versioned volumes don't modify the root volume from where they derive, and versioned volumes can also be the root of other volumes.

[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/docs/proposals/data-versioning.md?pixel)]()
