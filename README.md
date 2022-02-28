# liqo-broker

A pluggable resource broker for [Liqo](https://github.com/liqotech/liqo/).

It can be used to share the resources of a number of "provider" clusters and present a unified view to a "customer" cluster. The broker then transparently handles the offloading of workloads.

## Installation

### On the broker's cluster

 - Create a pod with this broker (use image `capacitorset/liqo-broker`), and create a service pointing to it.

 - Create a NamespaceOffloading in the `liqo` namespace with `podOffloadingStrategy: Local`, so that the `liqo-network-manager` service is reflected on the customer's cluster:
    
    ```yaml
    apiVersion: offloading.liqo.io/v1alpha1
    kind: NamespaceOffloading
    metadata:
        name: offloading
        namespace: liqo
    spec:
        namespaceMappingStrategy: DefaultName
        podOffloadingStrategy: Local
        clusterSelector:
            nodeSelectorTerms:
            - matchExpressions:
            - key: liqo.io/type
                operator: In
                values:
                - virtual-node
    ```

### On the customer's cluster

 - Before installing Liqo, set `virtualKubelet.imageName` to `capacitorset/liqo-broker-vk:latest` in the Helm values; if Liqo is already running, edit the `liqo-controller-manager` deployment to set `--kubelet-image=capacitorset/liqo-broker-vk:latest`.

 - Take note of the address of the service you reflected in the previous steps on the cluster. For example, if `kubectl get svc -A` shows that the service is called `liqo-network-manager` and belongs to the namespace `liqo-broker-b9d3d5`, then the address would be `liqo-network-manager.liqo-broker-b9d3d5.svc.cluster.local`.

 - Edit the `liqo-controller-manager` deployment to add the following command line flag: `--kubelet-extra-args=--remote-ipam=server=liqo-network-manager.liqo-broker-b9d3d5.svc.cluster.local` (where the argument is the address you found in the previous step).

## Usage

Simply create Liqo peerings as needed: the broker cluster will present a ResourceOffer that is the sum of all ResourceOffers from clusters with which it has outgoing peerings (the "provider" clusters). It will also react to workloads that are offloaded to the broker cluster by offloading them in turn to the provider clusters using a NamespaceOffloading.

## Credits

Based on a prototype by [Giuseppe Alicino](https://github.com/giuse2596).