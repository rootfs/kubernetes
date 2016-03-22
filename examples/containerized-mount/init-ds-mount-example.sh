#!/bin/sh
kubectl create -f mounter-daemonset.yaml 
kubectl create -f configMap.yaml
kubectl create -f ../glusterfs/glusterfs-endpoints.json 
kubectl create -f ../glusterfs/glusterfs-service.json
kubectl create -f ../glusterfs/glusterfs-pod.json
