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

package components

import (
	"fmt"
	"strings"

	"k8s.io/klog/v2"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/loader"
)

// ContainerdOptionsBuilder adds options for containerd to the model
type ContainerdOptionsBuilder struct {
	*OptionsContext
}

var _ loader.OptionsBuilder = &ContainerdOptionsBuilder{}

// BuildOptions is responsible for filling in the default setting for containerd daemon
func (b *ContainerdOptionsBuilder) BuildOptions(o interface{}) error {
	clusterSpec := o.(*kops.ClusterSpec)

	if clusterSpec.Containerd == nil {
		clusterSpec.Containerd = &kops.ContainerdConfig{}
	}

	containerd := clusterSpec.Containerd

	if clusterSpec.ContainerRuntime == "containerd" {
		if b.IsKubernetesLT("1.18") {
			klog.Warningf("kubernetes %s is untested with containerd", clusterSpec.KubernetesVersion)
		}

		// Set containerd based on Kubernetes version
		if fi.StringValue(containerd.Version) == "" {
			if b.IsKubernetesGTE("1.19") {
				containerd.Version = fi.String("1.4.1")
			} else if b.IsKubernetesGTE("1.18") {
				containerd.Version = fi.String("1.3.4")
			} else {
				return fmt.Errorf("containerd version is required")
			}
		}

		// Apply defaults for containerd running in container runtime mode
		containerd.LogLevel = fi.String("info")
		usesKubenet := UsesKubenet(clusterSpec.Networking)
		if clusterSpec.Networking != nil && usesKubenet {
			// Using containerd with Kubenet requires special configuration. This is a temporary backwards-compatible solution
			// and will be deprecated when Kubenet is deprecated:
			// https://github.com/containerd/cri/blob/master/docs/config.md#cni-config-template
			lines := []string{
				"version = 2",
				"[plugins]",
				"  [plugins.\"io.containerd.grpc.v1.cri\"]",
				"    [plugins.\"io.containerd.grpc.v1.cri\".cni]",
				"      conf_template = \"/etc/containerd/cni-config.template\"",
			}
			contents := strings.Join(lines, "\n")
			containerd.ConfigOverride = fi.String(contents)
		} else {
			containerd.ConfigOverride = fi.String("")
		}

	} else if clusterSpec.ContainerRuntime == "docker" {
		if fi.StringValue(containerd.Version) == "" {
			// Docker version should always be available
			if fi.StringValue(clusterSpec.Docker.Version) == "" {
				return fmt.Errorf("docker version is required")
			}

			// Set the containerd version for known Docker versions
			switch fi.StringValue(clusterSpec.Docker.Version) {
			case "19.03.13":
				containerd.Version = fi.String("1.3.7")
			case "19.03.8", "19.03.11":
				containerd.Version = fi.String("1.2.13")
			case "19.03.4":
				containerd.Version = fi.String("1.2.10")
			case "18.09.9":
				containerd.Version = fi.String("1.2.10")
			case "18.09.3":
				containerd.Version = fi.String("1.2.4")
			default:
				// Old version of docker, single package
				containerd.SkipInstall = true
				return nil
			}
		}

		// Apply defaults for containerd running in Docker mode
		containerd.LogLevel = fi.String("info")
		containerd.ConfigOverride = fi.String("disabled_plugins = [\"cri\"]\n")

	} else {
		// Unknown container runtime, should not install containerd
		containerd.SkipInstall = true
	}

	return nil
}
