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

	"k8s.io/kops/pkg/flagbuilder"
	"k8s.io/kops/pkg/k8scodecs"
	"k8s.io/kops/pkg/kubemanifest"
	"k8s.io/kops/pkg/rbac"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/nodeup/nodetasks"
	"k8s.io/kops/util/pkg/architectures"
	"k8s.io/kops/util/pkg/distributions"
	"k8s.io/kops/util/pkg/exec"
	"k8s.io/kops/util/pkg/proxy"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// KubeControllerManagerBuilder install kube-controller-manager (just the manifest at the moment)
type KubeControllerManagerBuilder struct {
	*NodeupModelContext
}

var _ fi.ModelBuilder = &KubeControllerManagerBuilder{}

// Build is responsible for configuring the kube-controller-manager
func (b *KubeControllerManagerBuilder) Build(c *fi.ModelBuilderContext) error {
	if !b.IsMaster {
		return nil
	}

	// Include the CA Key
	// @TODO: use a per-machine key?  use KMS?
	if err := b.BuildPrivateKeyTask(c, fi.CertificateIDCA, "ca.key", nil); err != nil {
		return err
	}

	{
		pod, err := b.buildPod()
		if err != nil {
			return fmt.Errorf("error building kube-controller-manager pod: %v", err)
		}

		manifest, err := k8scodecs.ToVersionedYaml(pod)
		if err != nil {
			return fmt.Errorf("error marshaling pod to yaml: %v", err)
		}

		c.AddTask(&nodetasks.File{
			Path:     "/etc/kubernetes/manifests/kube-controller-manager.manifest",
			Contents: fi.NewBytesResource(manifest),
			Type:     nodetasks.FileType_File,
		})
	}

	{
		c.AddTask(&nodetasks.File{
			Path:        "/var/log/kube-controller-manager.log",
			Contents:    fi.NewStringResource(""),
			Type:        nodetasks.FileType_File,
			Mode:        s("0400"),
			IfNotExists: true,
		})
	}

	// Add kubeconfig
	{
		kubeconfig := b.BuildIssuedKubeconfig("kube-controller-manager", nodetasks.PKIXName{CommonName: rbac.KubeControllerManager}, c)
		c.AddTask(&nodetasks.File{
			Path:     "/var/lib/kube-controller-manager/kubeconfig",
			Contents: kubeconfig,
			Type:     nodetasks.FileType_File,
			Mode:     s("0400"),
		})
	}

	return nil
}

// buildPod is responsible for building the kubernetes manifest for the controller-manager
func (b *KubeControllerManagerBuilder) buildPod() (*v1.Pod, error) {

	kcm := b.Cluster.Spec.KubeControllerManager
	kcm.RootCAFile = filepath.Join(b.PathSrvKubernetes(), "ca.crt")
	kcm.ServiceAccountPrivateKeyFile = filepath.Join(b.PathSrvKubernetes(), "service-account.key")

	flags, err := flagbuilder.BuildFlagsList(kcm)
	if err != nil {
		return nil, fmt.Errorf("error building kube-controller-manager flags: %v", err)
	}

	// Add cloud config file if needed
	if b.Cluster.Spec.CloudConfig != nil {
		flags = append(flags, "--cloud-config="+CloudConfigFilePath)
	}

	// Add kubeconfig flag
	flags = append(flags, "--kubeconfig="+"/var/lib/kube-controller-manager/kubeconfig")

	// Configure CA certificate to be used to sign keys
	flags = append(flags, []string{
		"--cluster-signing-cert-file=" + filepath.Join(b.PathSrvKubernetes(), "ca.crt"),
		"--cluster-signing-key-file=" + filepath.Join(b.PathSrvKubernetes(), "ca.key")}...)

	pod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-controller-manager",
			Namespace: "kube-system",
			Labels: map[string]string{
				"k8s-app": "kube-controller-manager",
			},
		},
		Spec: v1.PodSpec{
			HostNetwork: true,
		},
	}

	volumePluginDir := b.Cluster.Spec.Kubelet.VolumePluginDirectory

	// Ensure the Volume Plugin dir is mounted on the same path as the host machine so DaemonSet deployment is possible
	if volumePluginDir == "" {
		switch b.Distribution {
		case distributions.DistributionContainerOS:
			// Default is different on ContainerOS, see https://github.com/kubernetes/kubernetes/pull/58171
			volumePluginDir = "/home/kubernetes/flexvolume/"

		case distributions.DistributionFlatcar:
			// The /usr directory is read-only for Flatcar
			volumePluginDir = "/var/lib/kubelet/volumeplugins/"

		default:
			volumePluginDir = "/usr/libexec/kubernetes/kubelet-plugins/volume/exec/"
		}
	}

	// Add the volumePluginDir flag if provided in the kubelet spec, or set above based on the OS
	flags = append(flags, "--flex-volume-plugin-dir="+volumePluginDir)

	image := kcm.Image
	if b.Architecture != architectures.ArchitectureAmd64 {
		image = strings.Replace(image, "-amd64", "-"+string(b.Architecture), 1)
	}

	container := &v1.Container{
		Name:  "kube-controller-manager",
		Image: image,
		Env:   proxy.GetProxyEnvVars(b.Cluster.Spec.EgressProxy),
		LivenessProbe: &v1.Probe{
			Handler: v1.Handler{
				HTTPGet: &v1.HTTPGetAction{
					Host: "127.0.0.1",
					Path: "/healthz",
					Port: intstr.FromInt(10252),
				},
			},
			InitialDelaySeconds: 15,
			TimeoutSeconds:      15,
		},
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("100m"),
			},
		},
	}

	// Log both to docker and to the logfile
	addHostPathMapping(pod, container, "logfile", "/var/log/kube-controller-manager.log").ReadOnly = false
	if b.IsKubernetesGTE("1.15") {
		// From k8s 1.15, we use lighter containers that don't include shells
		// But they have richer logging support via klog
		container.Command = []string{"/usr/local/bin/kube-controller-manager"}
		container.Args = append(
			sortedStrings(flags),
			"--logtostderr=false", //https://github.com/kubernetes/klog/issues/60
			"--alsologtostderr",
			"--log-file=/var/log/kube-controller-manager.log")
	} else {
		container.Command = exec.WithTee(
			"/usr/local/bin/kube-controller-manager",
			sortedStrings(flags),
			"/var/log/kube-controller-manager.log")
	}

	for _, path := range b.SSLHostPaths() {
		name := strings.Replace(path, "/", "", -1)
		addHostPathMapping(pod, container, name, path)
	}

	// Add cloud config file if needed
	if b.Cluster.Spec.CloudConfig != nil {
		addHostPathMapping(pod, container, "cloudconfig", CloudConfigFilePath)
	}

	pathSrvKubernetes := b.PathSrvKubernetes()
	if pathSrvKubernetes != "" {
		addHostPathMapping(pod, container, "srvkube", pathSrvKubernetes)
	}

	addHostPathMapping(pod, container, "varlibkcm", "/var/lib/kube-controller-manager")

	addHostPathMapping(pod, container, "volplugins", volumePluginDir).ReadOnly = false

	pod.Spec.Containers = append(pod.Spec.Containers, *container)

	kubemanifest.MarkPodAsCritical(pod)
	kubemanifest.MarkPodAsClusterCritical(pod)

	return pod, nil
}
