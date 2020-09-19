/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/klog/v2"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/apis/kops/model"
	"k8s.io/kops/pkg/apis/kops/util"
	"k8s.io/kops/pkg/apis/nodeup"
	"k8s.io/kops/pkg/systemd"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/nodeup/nodetasks"
	"k8s.io/kops/util/pkg/architectures"
	"k8s.io/kops/util/pkg/distributions"
	"k8s.io/kops/util/pkg/vfs"
	"k8s.io/utils/mount"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/blang/semver/v4"
)

// NodeupModelContext is the context supplied the nodeup tasks
type NodeupModelContext struct {
	Architecture  architectures.Architecture
	Assets        *fi.AssetStore
	Cluster       *kops.Cluster
	ConfigBase    vfs.Path
	Distribution  distributions.Distribution
	InstanceGroup *kops.InstanceGroup
	KeyStore      fi.CAStore
	NodeupConfig  *nodeup.Config
	SecretStore   fi.SecretStore

	// IsMaster is true if the InstanceGroup has a role of master (populated by Init)
	IsMaster bool

	kubernetesVersion semver.Version
	bootstrapCerts    map[string]*nodetasks.BootstrapCert
}

// Init completes initialization of the object, for example pre-parsing the kubernetes version
func (c *NodeupModelContext) Init() error {
	k8sVersion, err := util.ParseKubernetesVersion(c.Cluster.Spec.KubernetesVersion)
	if err != nil || k8sVersion == nil {
		return fmt.Errorf("unable to parse KubernetesVersion %q", c.Cluster.Spec.KubernetesVersion)
	}
	c.kubernetesVersion = *k8sVersion
	c.bootstrapCerts = map[string]*nodetasks.BootstrapCert{}

	if c.NodeupConfig.InstanceGroupRole == kops.InstanceGroupRoleMaster {
		c.IsMaster = true
	}

	return nil
}

// SSLHostPaths returns the TLS paths for the distribution
func (c *NodeupModelContext) SSLHostPaths() []string {
	paths := []string{"/etc/ssl", "/etc/pki/tls", "/etc/pki/ca-trust"}

	switch c.Distribution {
	case distributions.DistributionFlatcar:
		// Because /usr is read-only on Flatcar, we can't have any new directories; docker will try (and fail) to create them
		// TODO: Just check if the directories exist?
		paths = append(paths, "/usr/share/ca-certificates")
	case distributions.DistributionContainerOS:
		paths = append(paths, "/usr/share/ca-certificates")
	default:
		paths = append(paths, "/usr/share/ssl", "/usr/ssl", "/usr/lib/ssl", "/usr/local/openssl", "/var/ssl", "/etc/openssl")
	}

	return paths
}

// VolumesServiceName is the name of the service which is downstream of any volume mounts
func (c *NodeupModelContext) VolumesServiceName() string {
	return c.EnsureSystemdSuffix("kops-volume-mounts")
}

// EnsureSystemdSuffix ensures that the hook name ends with a valid systemd unit file extension. If it
// doesn't, it adds ".service" for backwards-compatibility with older versions of Kops
func (c *NodeupModelContext) EnsureSystemdSuffix(name string) string {
	if !systemd.UnitFileExtensionValid(name) {
		name += ".service"
	}

	return name
}

// EnsureDirectory ensures the directory exists or creates it
func (c *NodeupModelContext) EnsureDirectory(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(path, 0755)
		}

		return err
	}

	if !st.IsDir() {
		return fmt.Errorf("path: %s already exists but is not a directory", path)
	}

	return nil
}

// IsMounted checks if the device is mount
func (c *NodeupModelContext) IsMounted(m mount.Interface, device, path string) (bool, error) {
	list, err := m.List()
	if err != nil {
		return false, err
	}

	for _, x := range list {
		if x.Device == device {
			klog.V(3).Infof("Found mountpoint device: %s, path: %s, type: %s", x.Device, x.Path, x.Type)
			if strings.TrimSuffix(x.Path, "/") == strings.TrimSuffix(path, "/") {
				return true, nil
			}
		}
	}

	return false, nil
}

// PathSrvKubernetes returns the path for the kubernetes service files
func (c *NodeupModelContext) PathSrvKubernetes() string {
	switch c.Distribution {
	case distributions.DistributionContainerOS:
		return "/etc/srv/kubernetes"
	default:
		return "/srv/kubernetes"
	}
}

// FileAssetsDefaultPath is the default location for assets which have no path
func (c *NodeupModelContext) FileAssetsDefaultPath() string {
	return filepath.Join(c.PathSrvKubernetes(), "assets")
}

// PathSrvSshproxy returns the path for the SSH proxy
func (c *NodeupModelContext) PathSrvSshproxy() string {
	switch c.Distribution {
	case distributions.DistributionContainerOS:
		return "/etc/srv/sshproxy"
	default:
		return "/srv/sshproxy"
	}
}

// KubeletBootstrapKubeconfig is the path the bootstrap config file
func (c *NodeupModelContext) KubeletBootstrapKubeconfig() string {
	path := c.Cluster.Spec.Kubelet.BootstrapKubeconfig

	if c.IsMaster {
		if c.Cluster.Spec.MasterKubelet != nil && c.Cluster.Spec.MasterKubelet.BootstrapKubeconfig != "" {
			path = c.Cluster.Spec.MasterKubelet.BootstrapKubeconfig
		}
	}

	if path != "" {
		return path
	}

	return "/var/lib/kubelet/bootstrap-kubeconfig"
}

// KubeletKubeConfig is the path of the kubelet kubeconfig file
func (c *NodeupModelContext) KubeletKubeConfig() string {
	return "/var/lib/kubelet/kubeconfig"
}

// BuildIssuedKubeconfig generates a kubeconfig with a locally issued client certificate.
func (c *NodeupModelContext) BuildIssuedKubeconfig(name string, subject nodetasks.PKIXName, ctx *fi.ModelBuilderContext) *fi.TaskDependentResource {
	issueCert := &nodetasks.IssueCert{
		Name:    name,
		Signer:  fi.CertificateIDCA,
		Type:    "client",
		Subject: subject,
	}
	ctx.AddTask(issueCert)
	certResource, keyResource, caResource := issueCert.GetResources()

	kubeConfig := &nodetasks.KubeConfig{
		Name: name,
		Cert: certResource,
		Key:  keyResource,
		CA:   caResource,
	}
	if c.IsMaster {
		// @note: use https even for local connections, so we can turn off the insecure port
		kubeConfig.ServerURL = "https://127.0.0.1"
	} else {
		kubeConfig.ServerURL = "https://" + c.Cluster.Spec.MasterInternalName
	}
	ctx.AddTask(kubeConfig)
	return kubeConfig.GetConfig()
}

// GetBootstrapCert requests a certificate keypair from kops-controller.
func (c *NodeupModelContext) GetBootstrapCert(name string) (cert, key fi.Resource) {
	b, ok := c.bootstrapCerts[name]
	if !ok {
		b = &nodetasks.BootstrapCert{
			Cert: &fi.TaskDependentResource{},
			Key:  &fi.TaskDependentResource{},
		}
		c.bootstrapCerts[name] = b
	}
	return b.Cert, b.Key
}

// BuildBootstrapKubeconfig generates a kubeconfig with a client certificate from either kops-controller or the state store.
func (c *NodeupModelContext) BuildBootstrapKubeconfig(name string, ctx *fi.ModelBuilderContext) (fi.Resource, error) {
	if c.UseKopsControllerForNodeBootstrap() {
		cert, key := c.GetBootstrapCert(name)

		ca, err := c.GetCert(fi.CertificateIDCA)
		if err != nil {
			return nil, err
		}

		kubeConfig := &nodetasks.KubeConfig{
			Name: name,
			Cert: cert,
			Key:  key,
			CA:   fi.NewBytesResource(ca),
		}
		if c.IsMaster {
			// @note: use https even for local connections, so we can turn off the insecure port
			kubeConfig.ServerURL = "https://127.0.0.1"
		} else {
			kubeConfig.ServerURL = "https://" + c.Cluster.Spec.MasterInternalName
		}

		err = ctx.EnsureTask(kubeConfig)
		if err != nil {
			return nil, err
		}

		return kubeConfig.GetConfig(), nil
	} else {
		ca, err := c.GetCert(fi.CertificateIDCA)
		if err != nil {
			return nil, err
		}

		cert, err := c.GetCert(name)
		if err != nil {
			return nil, err
		}

		key, err := c.GetPrivateKey(name)
		if err != nil {
			return nil, err
		}

		kubeConfig := &nodetasks.KubeConfig{
			Name: name,
			Cert: fi.NewBytesResource(cert),
			Key:  fi.NewBytesResource(key),
			CA:   fi.NewBytesResource(ca),
		}
		if c.IsMaster {
			// @note: use https even for local connections, so we can turn off the insecure port
			// This code path is used for the kubelet cert in Kubernetes 1.18 and earlier.
			kubeConfig.ServerURL = "https://127.0.0.1"
		} else {
			kubeConfig.ServerURL = "https://" + c.Cluster.Spec.MasterInternalName
		}

		err = kubeConfig.Run(nil)
		if err != nil {
			return nil, err
		}

		config, err := fi.ResourceAsBytes(kubeConfig.GetConfig())
		if err != nil {
			return nil, err
		}

		return fi.NewBytesResource(config), nil
	}
}

// IsKubernetesGTE checks if the version is greater-than-or-equal
func (c *NodeupModelContext) IsKubernetesGTE(version string) bool {
	if c.kubernetesVersion.Major == 0 {
		klog.Fatalf("kubernetesVersion not set (%s); Init not called", c.kubernetesVersion)
	}
	return util.IsKubernetesGTE(version, c.kubernetesVersion)
}

// IsKubernetesLT checks if the version is less-than
func (c *NodeupModelContext) IsKubernetesLT(version string) bool {
	if c.kubernetesVersion.Major == 0 {
		klog.Fatalf("kubernetesVersion not set (%s); Init not called", c.kubernetesVersion)
	}
	return !c.IsKubernetesGTE(version)
}

// UseEtcdManager checks if the etcd cluster has etcd-manager enabled
func (c *NodeupModelContext) UseEtcdManager() bool {
	for _, x := range c.Cluster.Spec.EtcdClusters {
		if x.Provider == kops.EtcdProviderTypeManager {
			return true
		}
	}

	return false
}

// UseEtcdTLS checks if the etcd cluster has TLS enabled bool
func (c *NodeupModelContext) UseEtcdTLS() bool {
	// @note: because we enforce that 'both' have to be enabled for TLS we only need to check one here.
	for _, x := range c.Cluster.Spec.EtcdClusters {
		if x.EnableEtcdTLS {
			return true
		}
	}

	return false
}

// UseVolumeMounts is used to check if we have volume mounts enabled as we need to
// insert requires and afters in various places
func (c *NodeupModelContext) UseVolumeMounts() bool {
	return len(c.NodeupConfig.VolumeMounts) > 0
}

// UseEtcdTLSAuth checks the peer-auth is set in both cluster
// @NOTE: in retrospect i think we should have consolidated the common config in the wrapper struct; it
// feels weird we set things like version, tls etc per cluster since they both have to be the same.
func (c *NodeupModelContext) UseEtcdTLSAuth() bool {
	if !c.UseEtcdTLS() {
		return false
	}

	for _, x := range c.Cluster.Spec.EtcdClusters {
		if x.EnableTLSAuth {
			return true
		}
	}

	return false
}

// UseKopsControllerForNodeBootstrap checks if nodeup should use kops-controller to bootstrap.
func (c *NodeupModelContext) UseKopsControllerForNodeBootstrap() bool {
	return model.UseKopsControllerForNodeBootstrap(c.Cluster)
}

// UseNodeAuthorization checks if have a node authorization policy
func (c *NodeupModelContext) UseNodeAuthorization() bool {
	return c.Cluster.Spec.NodeAuthorization != nil
}

// UseNodeAuthorizer checks if node authorization is enabled
func (c *NodeupModelContext) UseNodeAuthorizer() bool {
	if !c.UseNodeAuthorization() || !c.UseBootstrapTokens() {
		return false
	}

	return c.Cluster.Spec.NodeAuthorization.NodeAuthorizer != nil
}

// UsesSecondaryIP checks if the CNI in use attaches secondary interfaces to the host.
func (c *NodeupModelContext) UsesSecondaryIP() bool {
	return (c.Cluster.Spec.Networking.CNI != nil && c.Cluster.Spec.Networking.CNI.UsesSecondaryIP) || c.Cluster.Spec.Networking.AmazonVPC != nil || c.Cluster.Spec.Networking.LyftVPC != nil ||
		(c.Cluster.Spec.Networking.Cilium != nil && c.Cluster.Spec.Networking.Cilium.Ipam == kops.CiliumIpamEni)
}

// UseBootstrapTokens checks if we are using bootstrap tokens
func (c *NodeupModelContext) UseBootstrapTokens() bool {
	if c.IsMaster {
		return fi.BoolValue(c.Cluster.Spec.KubeAPIServer.EnableBootstrapAuthToken)
	}

	return c.Cluster.Spec.Kubelet != nil && c.Cluster.Spec.Kubelet.BootstrapKubeconfig != ""
}

// UseSecureKubelet checks if the kubelet api should be protected by a client certificate.
func (c *NodeupModelContext) UseSecureKubelet() bool {
	return c.NodeupConfig.KubeletConfig.AnonymousAuth != nil && !*c.NodeupConfig.KubeletConfig.AnonymousAuth
}

// KubectlPath returns distro based path for kubectl
func (c *NodeupModelContext) KubectlPath() string {
	kubeletCommand := "/usr/local/bin"
	if c.Distribution == distributions.DistributionFlatcar {
		kubeletCommand = "/opt/bin"
	}
	if c.Distribution == distributions.DistributionContainerOS {
		kubeletCommand = "/home/kubernetes/bin"
	}
	return kubeletCommand
}

// BuildCertificatePairTask creates the tasks to create the certificate and private key files.
func (c *NodeupModelContext) BuildCertificatePairTask(ctx *fi.ModelBuilderContext, key, path, filename string, owner *string) error {
	certificateName := filepath.Join(path, filename+".pem")
	keyName := filepath.Join(path, filename+"-key.pem")

	if err := c.BuildCertificateTask(ctx, key, certificateName, owner); err != nil {
		return err
	}

	return c.BuildPrivateKeyTask(ctx, key, keyName, owner)
}

// BuildCertificateTask builds a task to create a certificate file.
func (c *NodeupModelContext) BuildCertificateTask(ctx *fi.ModelBuilderContext, name, filename string, owner *string) error {
	cert, err := c.KeyStore.FindCert(name)
	if err != nil {
		return err
	}

	if cert == nil {
		return fmt.Errorf("certificate %q not found", name)
	}

	serialized, err := cert.AsString()
	if err != nil {
		return err
	}
	p := filename
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.PathSrvKubernetes(), filename)
	}

	ctx.AddTask(&nodetasks.File{
		Path:     p,
		Contents: fi.NewStringResource(serialized),
		Type:     nodetasks.FileType_File,
		Mode:     s("0600"),
		Owner:    owner,
	})

	return nil
}

// BuildPrivateKeyTask builds a task to create a private key file.
func (c *NodeupModelContext) BuildPrivateKeyTask(ctx *fi.ModelBuilderContext, name, filename string, owner *string) error {
	cert, err := c.KeyStore.FindPrivateKey(name)
	if err != nil {
		return err
	}

	if cert == nil {
		return fmt.Errorf("private key %q not found", name)
	}

	serialized, err := cert.AsString()
	if err != nil {
		return err
	}

	p := filename
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.PathSrvKubernetes(), filename)
	}

	ctx.AddTask(&nodetasks.File{
		Path:     p,
		Contents: fi.NewStringResource(serialized),
		Type:     nodetasks.FileType_File,
		Mode:     s("0600"),
		Owner:    owner,
	})

	return nil
}

// NodeName returns the name of the local Node, as it will be created in k8s
func (c *NodeupModelContext) NodeName() (string, error) {
	// This mirrors nodeutil.GetHostName
	hostnameOverride := c.Cluster.Spec.Kubelet.HostnameOverride

	if c.IsMaster && c.Cluster.Spec.MasterKubelet.HostnameOverride != "" {
		hostnameOverride = c.Cluster.Spec.MasterKubelet.HostnameOverride
	}

	nodeName, err := EvaluateHostnameOverride(hostnameOverride)
	if err != nil {
		return "", fmt.Errorf("error evaluating hostname: %v", err)
	}

	if nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			klog.Fatalf("Couldn't determine hostname: %v", err)
		}
		nodeName = hostname
	}

	return strings.ToLower(strings.TrimSpace(nodeName)), nil
}

// EvaluateHostnameOverride returns the hostname after replacing some well-known placeholders
func EvaluateHostnameOverride(hostnameOverride string) (string, error) {
	if hostnameOverride == "" || hostnameOverride == "@hostname" {
		return "", nil
	}
	k := strings.TrimSpace(hostnameOverride)
	k = strings.ToLower(k)

	if k != "@aws" {
		return hostnameOverride, nil
	}

	// We recognize @aws as meaning "the private DNS name from AWS", to generate this we need to get a few pieces of information
	azBytes, err := vfs.Context.ReadFile("metadata://aws/meta-data/placement/availability-zone")
	if err != nil {
		return "", fmt.Errorf("error reading availability zone from AWS metadata: %v", err)
	}

	instanceIDBytes, err := vfs.Context.ReadFile("metadata://aws/meta-data/instance-id")
	if err != nil {
		return "", fmt.Errorf("error reading instance-id from AWS metadata: %v", err)
	}
	instanceID := string(instanceIDBytes)

	config := aws.NewConfig()
	config = config.WithCredentialsChainVerboseErrors(true)

	s, err := session.NewSession(config)
	if err != nil {
		return "", fmt.Errorf("error starting new AWS session: %v", err)
	}

	svc := ec2.New(s, config.WithRegion(string(azBytes[:len(azBytes)-1])))

	result, err := svc.DescribeInstances(&ec2.DescribeInstancesInput{
		InstanceIds: []*string{&instanceID},
	})
	if err != nil {
		return "", fmt.Errorf("error describing instances: %v", err)
	}

	if len(result.Reservations) != 1 {
		return "", fmt.Errorf("too many reservations returned for the single instance-id")
	}

	if len(result.Reservations[0].Instances) != 1 {
		return "", fmt.Errorf("too many instances returned for the single instance-id")
	}
	return *(result.Reservations[0].Instances[0].PrivateDnsName), nil
}

// GetCert is a helper method to retrieve a certificate from the store
func (c *NodeupModelContext) GetCert(name string) ([]byte, error) {
	cert, err := c.KeyStore.FindCert(name)
	if err != nil {
		return []byte{}, fmt.Errorf("error fetching certificate: %v from keystore: %v", name, err)
	}
	if cert == nil {
		return []byte{}, fmt.Errorf("unable to find certificate: %s", name)
	}

	return cert.AsBytes()
}

// GetPrivateKey is a helper method to retrieve a private key from the store
func (c *NodeupModelContext) GetPrivateKey(name string) ([]byte, error) {
	key, err := c.KeyStore.FindPrivateKey(name)
	if err != nil {
		return []byte{}, fmt.Errorf("error fetching private key: %v from keystore: %v", name, err)
	}
	if key == nil {
		return []byte{}, fmt.Errorf("unable to find private key: %s", name)
	}

	return key.AsBytes()
}

func (b *NodeupModelContext) AddCNIBinAssets(c *fi.ModelBuilderContext, assetNames []string) error {
	for _, assetName := range assetNames {
		if err := b.addCNIBinAsset(c, assetName); err != nil {
			return err
		}
	}
	return nil
}

func (b *NodeupModelContext) addCNIBinAsset(c *fi.ModelBuilderContext, assetName string) error {
	assetPath := ""
	asset, err := b.Assets.Find(assetName, assetPath)
	if err != nil {
		return fmt.Errorf("error trying to locate asset %q: %v", assetName, err)
	}
	if asset == nil {
		return fmt.Errorf("unable to locate asset %q", assetName)
	}

	c.AddTask(&nodetasks.File{
		Path:     filepath.Join(b.CNIBinDir(), assetName),
		Contents: asset,
		Type:     nodetasks.FileType_File,
		Mode:     fi.String("0755"),
	})

	return nil
}

// UsesCNI checks if the cluster has CNI configured
func (c *NodeupModelContext) UsesCNI() bool {
	networking := c.Cluster.Spec.Networking
	if networking == nil || networking.Classic != nil {
		return false
	}

	return true
}

// CNIBinDir returns the path for the CNI binaries
func (c *NodeupModelContext) CNIBinDir() string {
	// We used to map this on a per-distro basis, but this can require CNI manifests to be distro aware
	return "/opt/cni/bin/"
}

// CNIConfDir returns the CNI directory
func (c *NodeupModelContext) CNIConfDir() string {
	return "/etc/cni/net.d/"
}
