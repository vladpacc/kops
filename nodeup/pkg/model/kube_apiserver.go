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
	"path/filepath"
	"strings"

	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/flagbuilder"
	"k8s.io/kops/pkg/k8scodecs"
	"k8s.io/kops/pkg/kubeconfig"
	"k8s.io/kops/pkg/kubemanifest"
	"k8s.io/kops/pkg/wellknownports"
	"k8s.io/kops/pkg/wellknownusers"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/nodeup/nodetasks"
	"k8s.io/kops/util/pkg/architectures"
	"k8s.io/kops/util/pkg/exec"
	"k8s.io/kops/util/pkg/proxy"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// PathAuthnConfig is the path to the custom webhook authentication config
const PathAuthnConfig = "/etc/kubernetes/authn.config"

// KubeAPIServerBuilder install kube-apiserver (just the manifest at the moment)
type KubeAPIServerBuilder struct {
	*NodeupModelContext
}

var _ fi.ModelBuilder = &KubeAPIServerBuilder{}

// Build is responsible for generating the configuration for the kube-apiserver
func (b *KubeAPIServerBuilder) Build(c *fi.ModelBuilderContext) error {
	if !b.IsMaster {
		return nil
	}

	if err := b.writeAuthenticationConfig(c); err != nil {
		return err
	}

	if b.Cluster.Spec.EncryptionConfig != nil {
		if *b.Cluster.Spec.EncryptionConfig {
			encryptionConfigPath := fi.String(filepath.Join(b.PathSrvKubernetes(), "encryptionconfig.yaml"))

			if b.IsKubernetesGTE("1.13") {
				b.Cluster.Spec.KubeAPIServer.EncryptionProviderConfig = encryptionConfigPath
			} else {
				b.Cluster.Spec.KubeAPIServer.ExperimentalEncryptionProviderConfig = encryptionConfigPath
			}

			key := "encryptionconfig"
			encryptioncfg, err := b.SecretStore.Secret(key)
			if err == nil {
				contents := string(encryptioncfg.Data)
				t := &nodetasks.File{
					Path:     *encryptionConfigPath,
					Contents: fi.NewStringResource(contents),
					Mode:     fi.String("600"),
					Type:     nodetasks.FileType_File,
				}
				c.AddTask(t)
			} else {
				return fmt.Errorf("encryptionConfig enabled, but could not load encryptionconfig secret: %v", err)
			}
		}
	}
	{
		pod, err := b.buildPod()
		if err != nil {
			return fmt.Errorf("error building kube-apiserver manifest: %v", err)
		}

		manifest, err := k8scodecs.ToVersionedYaml(pod)
		if err != nil {
			return fmt.Errorf("error marshaling manifest to yaml: %v", err)
		}

		c.AddTask(&nodetasks.File{
			Path:     "/etc/kubernetes/manifests/kube-apiserver.manifest",
			Contents: fi.NewBytesResource(manifest),
			Type:     nodetasks.FileType_File,
		})
	}

	// If we're using kube-apiserver-healthcheck, we need to set up the client cert etc
	if b.findHealthcheckManifest() != nil {
		if err := b.addHealthcheckSidecarTasks(c); err != nil {
			return err
		}
	}

	// @check if we are using secure client certificates for kubelet and grab the certificates
	if b.UseSecureKubelet() {
		issueCert := &nodetasks.IssueCert{
			Name:    "kubelet-api",
			Signer:  fi.CertificateIDCA,
			Type:    "client",
			Subject: nodetasks.PKIXName{CommonName: "kubelet-api"},
		}
		c.AddTask(issueCert)
		err := issueCert.AddFileTasks(c, b.PathSrvKubernetes(), "kubelet-api", "", nil)
		if err != nil {
			return err
		}
	}

	c.AddTask(&nodetasks.File{
		Path:        "/var/log/kube-apiserver.log",
		Contents:    fi.NewStringResource(""),
		Type:        nodetasks.FileType_File,
		Mode:        s("0400"),
		IfNotExists: true,
	})

	return nil
}

func (b *KubeAPIServerBuilder) writeAuthenticationConfig(c *fi.ModelBuilderContext) error {
	if b.Cluster.Spec.Authentication == nil || b.Cluster.Spec.Authentication.IsEmpty() {
		return nil
	}

	if b.Cluster.Spec.Authentication.Kopeio != nil {
		cluster := kubeconfig.KubectlCluster{
			Server: "http://127.0.0.1:9001/hooks/authn",
		}
		context := kubeconfig.KubectlContext{
			Cluster: "webhook",
			User:    "kube-apiserver",
		}

		config := kubeconfig.KubectlConfig{
			Kind:       "Config",
			ApiVersion: "v1",
		}
		config.Clusters = append(config.Clusters, &kubeconfig.KubectlClusterWithName{
			Name:    "webhook",
			Cluster: cluster,
		})
		config.Users = append(config.Users, &kubeconfig.KubectlUserWithName{
			Name: "kube-apiserver",
		})
		config.CurrentContext = "webhook"
		config.Contexts = append(config.Contexts, &kubeconfig.KubectlContextWithName{
			Name:    "webhook",
			Context: context,
		})

		manifest, err := kops.ToRawYaml(config)
		if err != nil {
			return fmt.Errorf("error marshaling authentication config to yaml: %v", err)
		}

		c.AddTask(&nodetasks.File{
			Path:     PathAuthnConfig,
			Contents: fi.NewBytesResource(manifest),
			Type:     nodetasks.FileType_File,
		})

		return nil
	}

	if b.Cluster.Spec.Authentication.Aws != nil {
		id := "aws-iam-authenticator"
		b.Cluster.Spec.KubeAPIServer.AuthenticationTokenWebhookConfigFile = fi.String(PathAuthnConfig)

		{
			caCertificate, err := b.NodeupModelContext.KeyStore.FindCert(fi.CertificateIDCA)
			if err != nil {
				return fmt.Errorf("error fetching AWS IAM Authentication CA certificate from keystore: %v", err)
			}
			if caCertificate == nil {
				return fmt.Errorf("AWS IAM  Authentication CA certificate %q not found", fi.CertificateIDCA)
			}

			cluster := kubeconfig.KubectlCluster{
				Server: "https://127.0.0.1:21362/authenticate",
			}
			context := kubeconfig.KubectlContext{
				Cluster: "aws-iam-authenticator",
				User:    "kube-apiserver",
			}

			cluster.CertificateAuthorityData, err = caCertificate.AsBytes()
			if err != nil {
				return fmt.Errorf("error encoding AWS IAM Authentication CA certificate: %v", err)
			}

			config := kubeconfig.KubectlConfig{}
			config.Clusters = append(config.Clusters, &kubeconfig.KubectlClusterWithName{
				Name:    "aws-iam-authenticator",
				Cluster: cluster,
			})
			config.Users = append(config.Users, &kubeconfig.KubectlUserWithName{
				Name: "kube-apiserver",
			})
			config.CurrentContext = "webhook"
			config.Contexts = append(config.Contexts, &kubeconfig.KubectlContextWithName{
				Name:    "webhook",
				Context: context,
			})

			manifest, err := kops.ToRawYaml(config)
			if err != nil {
				return fmt.Errorf("error marshaling authentication config to yaml: %v", err)
			}

			c.AddTask(&nodetasks.File{
				Path:     PathAuthnConfig,
				Contents: fi.NewBytesResource(manifest),
				Type:     nodetasks.FileType_File,
				Mode:     fi.String("600"),
			})
		}

		// We create user aws-iam-authenticator and hardcode its UID to 10000 as
		// that is the ID used inside the aws-iam-authenticator container.
		// The owner/group for the keypair to aws-iam-authenticator
		{
			c.AddTask(&nodetasks.UserTask{
				Name:  "aws-iam-authenticator",
				UID:   wellknownusers.AWSAuthenticator,
				Shell: "/sbin/nologin",
				Home:  "/srv/kubernetes/aws-iam-authenticator",
			})
		}

		{
			issueCert := &nodetasks.IssueCert{
				Name:    id,
				Signer:  fi.CertificateIDCA,
				Type:    "server",
				Subject: nodetasks.PKIXName{CommonName: id},
				AlternateNames: []string{
					"localhost",
					"127.0.0.1",
				},
			}
			c.AddTask(issueCert)
			certificate, privateKey, _ := issueCert.GetResources()

			c.AddTask(&nodetasks.File{
				Path:     "/srv/kubernetes/aws-iam-authenticator/cert.pem",
				Contents: certificate,
				Type:     nodetasks.FileType_File,
				Mode:     fi.String("600"),
				Owner:    fi.String("aws-iam-authenticator"),
				Group:    fi.String("aws-iam-authenticator"),
			})

			c.AddTask(&nodetasks.File{
				Path:     "/srv/kubernetes/aws-iam-authenticator/key.pem",
				Contents: privateKey,
				Type:     nodetasks.FileType_File,
				Mode:     fi.String("600"),
				Owner:    fi.String("aws-iam-authenticator"),
				Group:    fi.String("aws-iam-authenticator"),
			})
		}

		return nil
	}

	return fmt.Errorf("unrecognized authentication config %v", b.Cluster.Spec.Authentication)
}

// buildPod is responsible for generating the kube-apiserver pod and thus manifest file
func (b *KubeAPIServerBuilder) buildPod() (*v1.Pod, error) {
	kubeAPIServer := b.Cluster.Spec.KubeAPIServer

	// TODO pass the public key instead. We would first need to segregate the secrets better.
	kubeAPIServer.ServiceAccountKeyFile = append(kubeAPIServer.ServiceAccountKeyFile, filepath.Join(b.PathSrvKubernetes(), "service-account.key"))

	// Set the signing key if we're using Service Account Token VolumeProjection
	if kubeAPIServer.ServiceAccountSigningKeyFile == nil {
		if fi.StringValue(kubeAPIServer.ServiceAccountIssuer) != "" {
			s := filepath.Join(b.PathSrvKubernetes(), "service-account.key")
			kubeAPIServer.ServiceAccountSigningKeyFile = &s
		}
	}

	kubeAPIServer.ClientCAFile = filepath.Join(b.PathSrvKubernetes(), "ca.crt")
	kubeAPIServer.TLSCertFile = filepath.Join(b.PathSrvKubernetes(), "server.crt")
	kubeAPIServer.TLSPrivateKeyFile = filepath.Join(b.PathSrvKubernetes(), "server.key")

	// Support for basic auth was deprecated 1.16 and removed in 1.19
	// https://github.com/kubernetes/kubernetes/pull/89069
	if b.IsKubernetesLT("1.18") {
		kubeAPIServer.TokenAuthFile = filepath.Join(b.PathSrvKubernetes(), "known_tokens.csv")
		if kubeAPIServer.DisableBasicAuth == nil || !*kubeAPIServer.DisableBasicAuth {
			kubeAPIServer.BasicAuthFile = filepath.Join(b.PathSrvKubernetes(), "basic_auth.csv")
		}
	} else if b.IsKubernetesLT("1.19") {
		if kubeAPIServer.DisableBasicAuth != nil && !*kubeAPIServer.DisableBasicAuth {
			kubeAPIServer.BasicAuthFile = filepath.Join(b.PathSrvKubernetes(), "basic_auth.csv")
		}
	}

	if b.UseEtcdManager() && b.UseEtcdTLS() {
		basedir := "/etc/kubernetes/pki/kube-apiserver"
		kubeAPIServer.EtcdCAFile = filepath.Join(basedir, "etcd-ca.crt")
		kubeAPIServer.EtcdCertFile = filepath.Join(basedir, "etcd-client.crt")
		kubeAPIServer.EtcdKeyFile = filepath.Join(basedir, "etcd-client.key")
		kubeAPIServer.EtcdServers = []string{"https://127.0.0.1:4001"}
		kubeAPIServer.EtcdServersOverrides = []string{"/events#https://127.0.0.1:4002"}
	} else if b.UseEtcdTLS() {
		kubeAPIServer.EtcdCAFile = filepath.Join(b.PathSrvKubernetes(), "ca.crt")
		kubeAPIServer.EtcdCertFile = filepath.Join(b.PathSrvKubernetes(), "etcd-client.pem")
		kubeAPIServer.EtcdKeyFile = filepath.Join(b.PathSrvKubernetes(), "etcd-client-key.pem")
		kubeAPIServer.EtcdServers = []string{"https://127.0.0.1:4001"}
		kubeAPIServer.EtcdServersOverrides = []string{"/events#https://127.0.0.1:4002"}
	}

	// @check if we are using secure kubelet client certificates
	if b.UseSecureKubelet() {
		// @note we are making assumption were using the ones created by the pki model, not custom defined ones
		kubeAPIServer.KubeletClientCertificate = filepath.Join(b.PathSrvKubernetes(), "kubelet-api.crt")
		kubeAPIServer.KubeletClientKey = filepath.Join(b.PathSrvKubernetes(), "kubelet-api.key")
	}

	{
		certPath := filepath.Join(b.PathSrvKubernetes(), "apiserver-aggregator.crt")
		kubeAPIServer.ProxyClientCertFile = &certPath
		keyPath := filepath.Join(b.PathSrvKubernetes(), "apiserver-aggregator.key")
		kubeAPIServer.ProxyClientKeyFile = &keyPath
	}

	// APIServer aggregation options
	{
		cert, err := b.KeyStore.FindCert("apiserver-aggregator-ca")
		if err != nil {
			return nil, fmt.Errorf("apiserver aggregator CA cert lookup failed: %v", err.Error())
		}

		if cert != nil {
			certPath := filepath.Join(b.PathSrvKubernetes(), "apiserver-aggregator-ca.crt")
			kubeAPIServer.RequestheaderClientCAFile = certPath
		}
	}

	// @fixup: the admission controller migrated from --admission-control to --enable-admission-plugins, but
	// most people will still have c.Spec.KubeAPIServer.AdmissionControl references into their configuration we need
	// to fix up. A PR https://github.com/kubernetes/kops/pull/5221/ introduced the issue and since the command line
	// flags are mutually exclusive the API refuses to come up.
	{
		// @note: note sure if this is the best place to put it, I could place into the validation.go which has the benefit of
		// fixing up the manifests itself, but that feels VERY hacky
		// @note: it's fine to use AdmissionControl here and it's not populated by the model, thus the only data could have come from the cluster spec
		c := b.Cluster.Spec.KubeAPIServer
		if len(c.AdmissionControl) > 0 {
			c.EnableAdmissionPlugins = append([]string(nil), c.AdmissionControl...)
			c.AdmissionControl = []string{}
		}
	}

	//remove elements from the spec that are not enabled yet
	if b.Cluster.Spec.KubeAPIServer.AuditDynamicConfiguration != nil && !b.IsKubernetesGTE("1.13") {
		b.Cluster.Spec.KubeAPIServer.AuditDynamicConfiguration = nil
	}

	// build the kube-apiserver flags for the service
	flags, err := flagbuilder.BuildFlagsList(b.Cluster.Spec.KubeAPIServer)
	if err != nil {
		return nil, fmt.Errorf("error building kube-apiserver flags: %v", err)
	}

	// add cloud config file if needed
	if b.Cluster.Spec.CloudConfig != nil {
		flags = append(flags, fmt.Sprintf("--cloud-config=%s", CloudConfigFilePath))
	}

	pod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "kube-apiserver",
			Namespace:   "kube-system",
			Annotations: b.buildAnnotations(),
			Labels: map[string]string{
				"k8s-app": "kube-apiserver",
			},
		},
		Spec: v1.PodSpec{
			HostNetwork: true,
		},
	}

	useHealthcheckProxy := b.findHealthcheckManifest() != nil

	probeAction := &v1.HTTPGetAction{
		Host: "127.0.0.1",
		Path: "/healthz",
		Port: intstr.FromInt(wellknownports.KubeAPIServerHealthCheck),
	}
	if useHealthcheckProxy {
		// kube-apiserver-healthcheck sidecar container runs on port 3990
	} else if kubeAPIServer.InsecurePort != 0 {
		probeAction.Port = intstr.FromInt(int(kubeAPIServer.InsecurePort))
	} else if kubeAPIServer.SecurePort != 0 {
		probeAction.Port = intstr.FromInt(int(kubeAPIServer.SecurePort))
		probeAction.Scheme = v1.URISchemeHTTPS
	}

	requestCPU := resource.MustParse("150m")
	if b.Cluster.Spec.KubeAPIServer.CPURequest != "" {
		requestCPU = resource.MustParse(b.Cluster.Spec.KubeAPIServer.CPURequest)
	}

	image := kubeAPIServer.Image
	if b.Architecture != architectures.ArchitectureAmd64 {
		image = strings.Replace(image, "-amd64", "-"+string(b.Architecture), 1)
	}

	container := &v1.Container{
		Name:  "kube-apiserver",
		Image: image,
		Env:   proxy.GetProxyEnvVars(b.Cluster.Spec.EgressProxy),
		LivenessProbe: &v1.Probe{
			Handler: v1.Handler{
				HTTPGet: probeAction,
			},
			InitialDelaySeconds: 45,
			TimeoutSeconds:      15,
		},
		Ports: []v1.ContainerPort{
			{
				Name:          "https",
				ContainerPort: b.Cluster.Spec.KubeAPIServer.SecurePort,
				HostPort:      b.Cluster.Spec.KubeAPIServer.SecurePort,
			},
		},
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{
				v1.ResourceCPU: requestCPU,
			},
		},
	}

	if kubeAPIServer.InsecurePort != 0 {
		container.Ports = append(container.Ports, v1.ContainerPort{
			Name:          "local",
			ContainerPort: kubeAPIServer.InsecurePort,
			HostPort:      kubeAPIServer.InsecurePort,
		})
	}

	// Log both to docker and to the logfile
	addHostPathMapping(pod, container, "logfile", "/var/log/kube-apiserver.log").ReadOnly = false
	if b.IsKubernetesGTE("1.15") {
		// From k8s 1.15, we use lighter containers that don't include shells
		// But they have richer logging support via klog
		container.Command = []string{"/usr/local/bin/kube-apiserver"}
		container.Args = append(
			sortedStrings(flags),
			"--logtostderr=false", //https://github.com/kubernetes/klog/issues/60
			"--alsologtostderr",
			"--log-file=/var/log/kube-apiserver.log")
	} else {
		container.Command = exec.WithTee(
			"/usr/local/bin/kube-apiserver",
			sortedStrings(flags),
			"/var/log/kube-apiserver.log")
	}

	for _, path := range b.SSLHostPaths() {
		name := strings.Replace(path, "/", "", -1)
		addHostPathMapping(pod, container, name, path)
	}

	if b.UseEtcdManager() {
		volumeType := v1.HostPathDirectoryOrCreate
		addHostPathVolume(pod, container,
			v1.HostPathVolumeSource{
				Path: "/etc/kubernetes/pki/kube-apiserver",
				Type: &volumeType,
			},
			v1.VolumeMount{
				Name:     "pki",
				ReadOnly: false,
			})
	}

	// Add cloud config file if needed
	if b.Cluster.Spec.CloudConfig != nil {
		addHostPathMapping(pod, container, "cloudconfig", CloudConfigFilePath)
	}

	pathSrvKubernetes := b.PathSrvKubernetes()
	if pathSrvKubernetes != "" {
		addHostPathMapping(pod, container, "srvkube", pathSrvKubernetes)
	}

	pathSrvSshproxy := b.PathSrvSshproxy()
	if pathSrvSshproxy != "" {
		addHostPathMapping(pod, container, "srvsshproxy", pathSrvSshproxy)
	}

	auditLogPath := b.Cluster.Spec.KubeAPIServer.AuditLogPath
	// Don't mount a volume if the mount path is set to '-' for stdout logging
	// See https://kubernetes.io/docs/tasks/debug-application-cluster/audit/#audit-backends
	if auditLogPath != nil && *auditLogPath != "-" {
		// Mount the directory of the path instead, as kube-apiserver rotates the log by renaming the file.
		// Renaming is not possible when the file is mounted as the host path, and will return a
		// 'Device or resource busy' error
		auditLogPathDir := filepath.Dir(*auditLogPath)
		addHostPathMapping(pod, container, "auditlogpathdir", auditLogPathDir).ReadOnly = false
	}

	if b.Cluster.Spec.Authentication != nil {
		if b.Cluster.Spec.Authentication.Kopeio != nil || b.Cluster.Spec.Authentication.Aws != nil {
			addHostPathMapping(pod, container, "authn-config", PathAuthnConfig)
		}
	}

	pod.Spec.Containers = append(pod.Spec.Containers, *container)

	kubemanifest.MarkPodAsCritical(pod)
	kubemanifest.MarkPodAsClusterCritical(pod)

	if useHealthcheckProxy {
		if err := b.addHealthcheckSidecar(pod); err != nil {
			return nil, err
		}
	}

	return pod, nil
}

func (b *KubeAPIServerBuilder) buildAnnotations() map[string]string {
	annotations := make(map[string]string)

	if b.Cluster.Spec.API != nil {
		if b.Cluster.Spec.API.LoadBalancer == nil || !b.Cluster.Spec.API.LoadBalancer.UseForInternalApi {
			annotations["dns.alpha.kubernetes.io/internal"] = b.Cluster.Spec.MasterInternalName
		}

		if b.Cluster.Spec.API.DNS != nil {
			annotations["dns.alpha.kubernetes.io/external"] = b.Cluster.Spec.MasterPublicName
		}
	}

	return annotations
}
