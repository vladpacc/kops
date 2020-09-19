# Cilium
* [Cilium](http://docs.cilium.io)

The Cilium CNI uses a Linux kernel technology called BPF, which enables the dynamic insertion of powerful security visibility and control logic within the Linux kernel.

## Installing Cilium on a new Cluster

To use the Cilium, specify the following in the cluster spec.

```yaml
  networking:
    cilium: {}
```

The following command sets up a cluster using Cilium.

```sh
export ZONES=mylistofzones
kops create cluster \
  --zones $ZONES \
  --networking cilium\
  --yes \
  --name cilium.example.com
```

## Configuring Cilium

### Using etcd for agent state sync

This feature is in beta state as of kops 1.18.

By default, Cilium will use CRDs for synchronizing agent state. This can cause performance problems on larger clusters. As of kops 1.18, kops can manage an etcd cluster using etcd-manager dedicated for cilium agent state sync. The [Cilium docs](https://docs.cilium.io/en/stable/gettingstarted/k8s-install-external-etcd/) contains recommendations for when this must be enabled.

For new clusters you can use the `cilium-etcd` networking provider:

```sh
export ZONES=mylistofzones
kops create cluster \
  --zones $ZONES \
  --networking cilium-etcd \
  --yes \
  --name cilium.example.com
```

For existing clusters, add the following to `spec.etcdClusters`:
Make sure `instanceGroup` match the other etcd clusters.

```yaml
  - etcdMembers:
    - instanceGroup: master-az-1a
      name: a
    - instanceGroup: master-az-1b
      name: b
    - instanceGroup: master-az-1c
      name: c
    name: cilium
```

It is important that you perform a rolling update on the entire cluster so that all the nodes can connect to the new etcd cluster.

```sh
kops update cluster
kops update cluster --yes
kops rolling-update cluster --force --yes

```

Then enable etcd as kvstore:

```yaml
  networking:
    cilium:
      etcdManaged: true
```

### Enabling BPF NodePort

As of kops 1.19, BPF NodePort is enabled by default for new clusters if the kubernetes version is 1.12 or newer. It can be safely enabled as of kops 1.18.

In this mode, the cluster is fully functional without kube-proxy, with Cilium replacing kube-proxy's NodePort implementation using BPF.
Read more about this in the [Cilium docs](https://docs.cilium.io/en/stable/gettingstarted/nodeport/)

Be aware that you need to use an AMI with at least Linux 4.19.57 for this feature to work.

Also be aware that while enabling this on an existing cluster is safe, disabling this is disruptive and requires you to run `kops rolling-upgrade cluster --cloudonly`.

```yaml
  kubeProxy:
    enabled: false
  networking:
    cilium:
      enableNodePort: true
```

If you are migrating an existing cluster, you need to manually roll the cilium DaemonSet before rolling the cluster:

```
kops update cluster
kops update cluster --yes
kubectl rollout restart ds/cilium -n kube-system
kops rolling-update cluster --yes
```

### Enabling Cilium ENI IPAM

This feature is in beta state as of kops 1.18.

As of Kops 1.18, you can have Cilium provision AWS managed adresses and attach them directly to Pods much like Lyft VPC and AWS VPC. See [the Cilium docs for more information](https://docs.cilium.io/en/v1.6/concepts/ipam/eni/)

When using ENI IPAM you need to disable masquerading in Cilium as well.

```yaml
  networking:
    cilium:
      disableMasquerade: true
      ipam: eni
```

Note that since Cilium Operator is the entity that interacts with the EC2 API to provision and attaching ENIs, we force it to run on the master nodes when this IPAM is used.

Also note that this feature has only been tested on the default kops AMIs.

#### Enabling Encryption in Cilium
{{ kops_feature_table(kops_added_default='1.19', k8s_min='1.17') }}

As of Kops 1.19, it is possible to enable encryption for Cilium agent.
In order to enable encryption, you must first generate the pre-shared key using this command:
```bash
cat <<EOF | kops create secret ciliumpassword -f -
keys: $(echo "3 rfc4106(gcm(aes)) $(echo $(dd if=/dev/urandom count=20 bs=1 2> /dev/null| xxd -p -c 64)) 128")
EOF
```
The above command will create a dedicated secret for cilium and store it in the Kops secret store.
Once the secret has been created, encryption can be enabled by setting `enableEncryption` option in `spec.networking.cilium` to `true`:
```yaml
  networking:
    cilium:
      enableEncryption: true
```


## Getting help

For problems with deploying Cilium please post an issue to Github:

- [Cilium Issues](https://github.com/cilium/cilium/issues)

For support with Cilium Network Policies you can reach out on Slack or Github:

- [Cilium Github](https://github.com/cilium/cilium)
- [Cilium Slack](https://cilium.io/slack)
