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
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"strings"

	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/apis/kops/util"
	"k8s.io/kops/pkg/assets"
	"k8s.io/kops/pkg/k8sversion"
	"k8s.io/kops/upup/pkg/fi/cloudup/gce"
	"k8s.io/kops/util/pkg/vfs"

	"github.com/blang/semver/v4"
	"k8s.io/klog/v2"
)

// OptionsContext is the context object for options builders
type OptionsContext struct {
	ClusterName string

	KubernetesVersion semver.Version

	AssetBuilder *assets.AssetBuilder
}

func (c *OptionsContext) IsKubernetesGTE(version string) bool {
	return util.IsKubernetesGTE(version, c.KubernetesVersion)
}

func (c *OptionsContext) IsKubernetesLT(version string) bool {
	return !c.IsKubernetesGTE(version)
}

// UsesKubenet returns true if our networking is derived from kubenet
func UsesKubenet(networking *kops.NetworkingSpec) bool {
	if networking == nil {
		panic("no networking mode set")
	}
	if networking.Kubenet != nil {
		return true
	} else if networking.GCE != nil {
		// GCE IP Alias networking is based on kubenet
		return true
	} else if networking.External != nil {
		// external is based on kubenet
		return true
	} else if networking.Kopeio != nil {
		// Kopeio is based on kubenet / external
		return true
	}

	return false

}

// UsesCNI returns true if the networking provider is a CNI plugin
func UsesCNI(networking *kops.NetworkingSpec) bool {
	// Kubenet and CNI are the only kubelet networking plugins right now.
	return !UsesKubenet(networking)
}

func WellKnownServiceIP(clusterSpec *kops.ClusterSpec, id int) (net.IP, error) {
	_, cidr, err := net.ParseCIDR(clusterSpec.ServiceClusterIPRange)
	if err != nil {
		return nil, fmt.Errorf("error parsing ServiceClusterIPRange %q: %v", clusterSpec.ServiceClusterIPRange, err)
	}

	ip4 := cidr.IP.To4()
	if ip4 != nil {
		n := binary.BigEndian.Uint32(ip4)
		n += uint32(id)
		serviceIP := make(net.IP, len(ip4))
		binary.BigEndian.PutUint32(serviceIP, n)
		return serviceIP, nil
	}

	ip6 := cidr.IP.To16()
	if ip6 != nil {
		baseIPInt := big.NewInt(0)
		baseIPInt.SetBytes(ip6)
		serviceIPInt := big.NewInt(0)
		serviceIPInt.Add(big.NewInt(int64(id)), baseIPInt)
		serviceIP := make(net.IP, len(ip6))
		serviceIPBytes := serviceIPInt.Bytes()
		for i := range serviceIPBytes {
			serviceIP[len(serviceIP)-len(serviceIPBytes)+i] = serviceIPBytes[i]
		}
		return serviceIP, nil
	}

	return nil, fmt.Errorf("unexpected IP address type for ServiceClusterIPRange: %s", clusterSpec.ServiceClusterIPRange)
}

func IsBaseURL(kubernetesVersion string) bool {
	return strings.HasPrefix(kubernetesVersion, "http:") || strings.HasPrefix(kubernetesVersion, "https:") || strings.HasPrefix(kubernetesVersion, "memfs:")
}

// Image returns the docker image name for the specified component
func Image(component string, clusterSpec *kops.ClusterSpec, assetsBuilder *assets.AssetBuilder) (string, error) {
	if assetsBuilder == nil {
		return "", fmt.Errorf("unable to parse assets as assetBuilder is not defined")
	}

	kubernetesVersion, err := k8sversion.Parse(clusterSpec.KubernetesVersion)
	if err != nil {
		return "", err
	}

	imageName := component

	if !IsBaseURL(clusterSpec.KubernetesVersion) {
		image := "k8s.gcr.io/" + imageName + ":" + "v" + kubernetesVersion.String()

		image, err := assetsBuilder.RemapImage(image)
		if err != nil {
			return "", fmt.Errorf("unable to remap container %q: %v", image, err)
		}
		return image, nil
	}

	// The simple name is valid when pulling (before 1.16 it was
	// only amd64, as of 1.16 it is a manifest list).  But if we
	// are loading from a tarfile then the image is tagged with
	// the architecture suffix.
	//
	// i.e. k8s.gcr.io/kube-apiserver:v1.16.0 is a manifest list
	// and we _can_ also pull
	// k8s.gcr.io/kube-apiserver-amd64:v1.16.0 directly.  But if
	// we load https://.../v1.16.0/amd64/kube-apiserver.tar then
	// the image inside that tar file is named
	// "k8s.gcr.io/kube-apiserver-amd64:v1.16.0"
	//
	// But ... this is only the case from 1.16 on...
	if kubernetesVersion.IsGTE("1.16") {
		imageName += "-amd64"
	}

	baseURL := clusterSpec.KubernetesVersion
	baseURL = strings.TrimSuffix(baseURL, "/")

	tagURL := baseURL + "/bin/linux/amd64/" + component + ".docker_tag"
	klog.V(2).Infof("Downloading docker tag for %s from: %s", component, tagURL)

	b, err := vfs.Context.ReadFile(tagURL)
	if err != nil {
		return "", fmt.Errorf("error reading tag file %q: %v", tagURL, err)
	}
	tag := strings.TrimSpace(string(b))
	klog.V(2).Infof("Found tag %q for %q", tag, component)

	image := "k8s.gcr.io/" + imageName + ":" + tag

	return image, nil
}

func GCETagForRole(clusterName string, role kops.InstanceGroupRole) string {
	return gce.SafeClusterName(clusterName) + "-" + gce.GceLabelNameRolePrefix + strings.ToLower(string(role))
}
