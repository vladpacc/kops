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

package kubeconfig

import (
	"crypto/x509/pkix"
	"fmt"
	"os/user"
	"sort"
	"time"

	"k8s.io/klog/v2"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/apis/kops/util"
	"k8s.io/kops/pkg/dns"
	"k8s.io/kops/pkg/pki"
	"k8s.io/kops/pkg/rbac"
	"k8s.io/kops/upup/pkg/fi"
)

const DefaultKubecfgAdminLifetime = 18 * time.Hour

func BuildKubecfg(cluster *kops.Cluster, keyStore fi.Keystore, secretStore fi.SecretStore, status kops.StatusStore, admin time.Duration, configUser string, internal bool, kopsStateStore string, useKopsAuthenticationPlugin bool) (*KubeconfigBuilder, error) {
	clusterName := cluster.ObjectMeta.Name

	var master string
	if internal {
		master = cluster.Spec.MasterInternalName
		if master == "" {
			master = "api.internal." + clusterName
		}
	} else {
		master = cluster.Spec.MasterPublicName
		if master == "" {
			master = "api." + clusterName
		}
	}

	server := "https://" + master

	// We use the LoadBalancer where we know the master DNS name is otherwise unreachable
	useELBName := false

	// If the master DNS is a gossip DNS name; there's no way that name can resolve outside the cluster
	if dns.IsGossipHostname(master) {
		useELBName = true
	}

	// If the DNS is set up as a private HostedZone, but here we have to be
	// careful that we aren't accessing the API over DirectConnect (or a VPN).
	// We differentiate using the heuristic that if we have an internal ELB
	// we are likely connected directly to the VPC.
	privateDNS := cluster.Spec.Topology != nil && cluster.Spec.Topology.DNS.Type == kops.DNSTypePrivate
	internalELB := cluster.Spec.API != nil && cluster.Spec.API.LoadBalancer != nil && cluster.Spec.API.LoadBalancer.Type == kops.LoadBalancerTypeInternal
	if privateDNS && !internalELB {
		useELBName = true
	}

	if useELBName {
		ingresses, err := status.GetApiIngressStatus(cluster)
		if err != nil {
			return nil, fmt.Errorf("error getting ingress status: %v", err)
		}

		var targets []string
		for _, ingress := range ingresses {
			if ingress.Hostname != "" {
				targets = append(targets, ingress.Hostname)
			}
			if ingress.IP != "" {
				targets = append(targets, ingress.IP)
			}
		}

		sort.Strings(targets)
		if len(targets) == 0 {
			klog.Warningf("Did not find API endpoint for gossip hostname; may not be able to reach cluster")
		} else {
			if len(targets) != 1 {
				klog.Warningf("Found multiple API endpoints (%v), choosing arbitrarily", targets)
			}
			server = "https://" + targets[0]
		}
	}

	b := NewKubeconfigBuilder()

	b.Context = clusterName
	b.Server = server

	// add the CA Cert to the kubeconfig only if we didn't specify a SSL cert for the LB or are targeting the internal DNS name
	if cluster.Spec.API == nil || cluster.Spec.API.LoadBalancer == nil || cluster.Spec.API.LoadBalancer.SSLCertificate == "" || internal {
		cert, _, _, err := keyStore.FindKeypair(fi.CertificateIDCA)
		if err != nil {
			return nil, fmt.Errorf("error fetching CA keypair: %v", err)
		}
		if cert != nil {
			b.CACert, err = cert.AsBytes()
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("cannot find CA certificate")
		}
	}

	if admin != 0 {
		cn := "kubecfg"
		user, err := user.Current()
		if err != nil || user == nil {
			klog.Infof("unable to get user: %v", err)
		} else {
			cn += "-" + user.Name
		}

		req := pki.IssueCertRequest{
			Signer: fi.CertificateIDCA,
			Type:   "client",
			Subject: pkix.Name{
				CommonName:   cn,
				Organization: []string{rbac.SystemPrivilegedGroup},
			},
			Validity: admin,
		}
		cert, privateKey, _, err := pki.IssueCert(&req, keyStore)
		if err != nil {
			return nil, err
		}
		b.ClientCert, err = cert.AsBytes()
		if err != nil {
			return nil, err
		}
		b.ClientKey, err = privateKey.AsBytes()
		if err != nil {
			return nil, err
		}
	}

	if useKopsAuthenticationPlugin {
		b.AuthenticationExec = []string{
			"kops",
			"helpers",
			"kubectl-auth",
			"--cluster=" + clusterName,
			"--state=" + kopsStateStore,
		}
	}

	b.Server = server

	k8sVersion, err := util.ParseKubernetesVersion(cluster.Spec.KubernetesVersion)
	if err != nil || k8sVersion == nil {
		klog.Warningf("unable to parse KubernetesVersion %q", cluster.Spec.KubernetesVersion)
		k8sVersion, _ = util.ParseKubernetesVersion("1.0.0")
	}

	basicAuthEnabled := false
	if !util.IsKubernetesGTE("1.18", *k8sVersion) {
		if cluster.Spec.KubeAPIServer == nil || cluster.Spec.KubeAPIServer.DisableBasicAuth == nil || !*cluster.Spec.KubeAPIServer.DisableBasicAuth {
			basicAuthEnabled = true
		}
	} else if !util.IsKubernetesGTE("1.19", *k8sVersion) {
		if cluster.Spec.KubeAPIServer != nil && cluster.Spec.KubeAPIServer.DisableBasicAuth != nil && !*cluster.Spec.KubeAPIServer.DisableBasicAuth {
			basicAuthEnabled = true
		}
	}

	if basicAuthEnabled && secretStore != nil {
		secret, err := secretStore.FindSecret("kube")
		if err != nil {
			return nil, err
		}
		if secret != nil {
			b.KubeUser = "admin"
			b.KubePassword = string(secret.Data)
		}
	}

	if configUser == "" {
		b.User = cluster.ObjectMeta.Name
	} else {
		b.User = configUser
	}

	return b, nil
}
