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

package validation

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/upup/pkg/fi"
)

func Test_Validate_DNS(t *testing.T) {
	for _, name := range []string{"test.-", "!", "-"} {
		errs := validation.IsDNS1123Subdomain(name)
		if len(errs) == 0 {
			t.Fatalf("Expected errors validating name %q", name)
		}
	}
}

func TestValidateCIDR(t *testing.T) {
	grid := []struct {
		Input          string
		ExpectedErrors []string
		ExpectedDetail string
	}{
		{
			Input: "192.168.0.1/32",
		},
		{
			Input:          "192.168.0.1",
			ExpectedErrors: []string{"Invalid value::CIDR"},
			ExpectedDetail: "Could not be parsed as a CIDR (did you mean \"192.168.0.1/32\")",
		},
		{
			Input:          "10.128.0.0/8",
			ExpectedErrors: []string{"Invalid value::CIDR"},
			ExpectedDetail: "Network contains bits outside prefix (did you mean \"10.0.0.0/8\")",
		},
		{
			Input:          "",
			ExpectedErrors: []string{"Invalid value::CIDR"},
		},
		{
			Input:          "invalid.example.com",
			ExpectedErrors: []string{"Invalid value::CIDR"},
			ExpectedDetail: "Could not be parsed as a CIDR",
		},
	}
	for _, g := range grid {
		errs := validateCIDR(g.Input, field.NewPath("CIDR"))

		testErrors(t, g.Input, errs, g.ExpectedErrors)

		if g.ExpectedDetail != "" {
			found := false
			for _, err := range errs {
				if err.Detail == g.ExpectedDetail {
					found = true
				}
			}
			if !found {
				for _, err := range errs {
					t.Logf("found detail: %q", err.Detail)
				}

				t.Errorf("did not find expected error %q", g.ExpectedDetail)
			}
		}
	}
}

func testErrors(t *testing.T, context interface{}, actual field.ErrorList, expectedErrors []string) {
	if len(expectedErrors) == 0 {
		if len(actual) != 0 {
			t.Errorf("unexpected errors from %q: %v", context, actual)
		}
	} else {
		errStrings := sets.NewString()
		for _, err := range actual {
			errStrings.Insert(err.Type.String() + "::" + err.Field)
		}

		for _, expected := range expectedErrors {
			if !errStrings.Has(expected) {
				t.Errorf("expected error %v from %v, was not found in %q", expected, context, errStrings.List())
			}
		}
	}
}

func TestValidateSubnets(t *testing.T) {
	grid := []struct {
		Input          []kops.ClusterSubnetSpec
		ExpectedErrors []string
	}{
		{
			Input: []kops.ClusterSubnetSpec{
				{Name: "a"},
			},
		},
		{
			Input: []kops.ClusterSubnetSpec{
				{Name: "a", CIDR: "10.0.0.0/8"},
			},
		},
		{
			Input: []kops.ClusterSubnetSpec{
				{Name: ""},
			},
			ExpectedErrors: []string{"Required value::subnets[0].name"},
		},
		{
			Input: []kops.ClusterSubnetSpec{
				{Name: "a"},
				{Name: "a"},
			},
			ExpectedErrors: []string{"Duplicate value::subnets[1].name"},
		},
		{
			Input: []kops.ClusterSubnetSpec{
				{Name: "a", ProviderID: "a"},
				{Name: "b", ProviderID: "b"},
			},
		},
		{
			Input: []kops.ClusterSubnetSpec{
				{Name: "a", ProviderID: "a"},
				{Name: "b", ProviderID: ""},
			},
			ExpectedErrors: []string{"Forbidden::subnets[1].id"},
		},
		{
			Input: []kops.ClusterSubnetSpec{
				{Name: "a", CIDR: "10.128.0.0/8"},
			},
			ExpectedErrors: []string{"Invalid value::subnets[0].cidr"},
		},
	}
	for _, g := range grid {
		errs := validateSubnets(g.Input, field.NewPath("subnets"))

		testErrors(t, g.Input, errs, g.ExpectedErrors)
	}
}

func TestValidateKubeAPIServer(t *testing.T) {
	str := "foobar"
	authzMode := "RBAC,Webhook"

	grid := []struct {
		Input          kops.KubeAPIServerConfig
		ExpectedErrors []string
		ExpectedDetail string
	}{
		{
			Input: kops.KubeAPIServerConfig{
				ProxyClientCertFile: &str,
			},
			ExpectedErrors: []string{
				"Forbidden::KubeAPIServer",
			},
			ExpectedDetail: "proxyClientCertFile and proxyClientKeyFile must both be specified (or neither)",
		},
		{
			Input: kops.KubeAPIServerConfig{
				ProxyClientKeyFile: &str,
			},
			ExpectedErrors: []string{
				"Forbidden::KubeAPIServer",
			},
			ExpectedDetail: "proxyClientCertFile and proxyClientKeyFile must both be specified (or neither)",
		},
		{
			Input: kops.KubeAPIServerConfig{
				ServiceNodePortRange: str,
			},
			ExpectedErrors: []string{
				"Invalid value::KubeAPIServer.serviceNodePortRange",
			},
		},
		{
			Input: kops.KubeAPIServerConfig{
				AuthorizationMode: &authzMode,
			},
			ExpectedErrors: []string{
				"Required value::KubeAPIServer.authorizationWebhookConfigFile",
			},
			ExpectedDetail: "Authorization mode Webhook requires authorizationWebhookConfigFile to be specified",
		},
	}
	for _, g := range grid {
		cluster := &kops.Cluster{
			Spec: kops.ClusterSpec{
				KubernetesVersion: "1.16.0",
			},
		}
		errs := validateKubeAPIServer(&g.Input, cluster, field.NewPath("KubeAPIServer"))

		testErrors(t, g.Input, errs, g.ExpectedErrors)

		if g.ExpectedDetail != "" {
			found := false
			for _, err := range errs {
				if err.Detail == g.ExpectedDetail {
					found = true
				}
			}
			if !found {
				for _, err := range errs {
					t.Logf("found detail: %q", err.Detail)
				}

				t.Errorf("did not find expected error %q", g.ExpectedDetail)
			}
		}
	}
}

func Test_Validate_DockerConfig_Storage(t *testing.T) {
	for _, name := range []string{"aufs", "zfs", "overlay"} {
		config := &kops.DockerConfig{Storage: &name}
		errs := validateDockerConfig(config, field.NewPath("docker"))
		if len(errs) != 0 {
			t.Fatalf("Unexpected errors validating DockerConfig %q", errs)
		}
	}

	for _, name := range []string{"overlayfs", "", "au"} {
		config := &kops.DockerConfig{Storage: &name}
		errs := validateDockerConfig(config, field.NewPath("docker"))
		if len(errs) != 1 {
			t.Fatalf("Expected errors validating DockerConfig %+v", config)
		}
		if errs[0].Field != "docker.storage" || errs[0].Type != field.ErrorTypeNotSupported {
			t.Fatalf("Not the expected error validating DockerConfig %q", errs)
		}
	}
}

func Test_Validate_Networking_Flannel(t *testing.T) {

	grid := []struct {
		Input          kops.FlannelNetworkingSpec
		ExpectedErrors []string
	}{
		{
			Input: kops.FlannelNetworkingSpec{
				Backend: "udp",
			},
		},
		{
			Input: kops.FlannelNetworkingSpec{
				Backend: "vxlan",
			},
		},
		{
			Input: kops.FlannelNetworkingSpec{
				Backend: "",
			},
			ExpectedErrors: []string{"Required value::networking.flannel.backend"},
		},
		{
			Input: kops.FlannelNetworkingSpec{
				Backend: "nope",
			},
			ExpectedErrors: []string{"Unsupported value::networking.flannel.backend"},
		},
	}
	for _, g := range grid {
		networking := &kops.NetworkingSpec{}
		networking.Flannel = &g.Input

		cluster := &kops.Cluster{}
		cluster.Spec.Networking = networking

		errs := validateNetworking(cluster, networking, field.NewPath("networking"))
		testErrors(t, g.Input, errs, g.ExpectedErrors)
	}
}

func Test_Validate_AdditionalPolicies(t *testing.T) {
	grid := []struct {
		Input          map[string]string
		ExpectedErrors []string
	}{
		{
			Input: map[string]string{},
		},
		{
			Input: map[string]string{
				"master": `[ { "Action": [ "s3:GetObject" ], "Resource": [ "*" ], "Effect": "Allow" } ]`,
			},
		},
		{
			Input: map[string]string{
				"notarole": `[ { "Action": [ "s3:GetObject" ], "Resource": [ "*" ], "Effect": "Allow" } ]`,
			},
			ExpectedErrors: []string{"Unsupported value::spec.additionalPolicies"},
		},
		{
			Input: map[string]string{
				"master": `badjson`,
			},
			ExpectedErrors: []string{"Invalid value::spec.additionalPolicies[master]"},
		},
		{
			Input: map[string]string{
				"master": `[ { "Action": [ "s3:GetObject" ], "Resource": [ "*" ] } ]`,
			},
			ExpectedErrors: []string{"Required value::spec.additionalPolicies[master][0].Effect"},
		},
		{
			Input: map[string]string{
				"master": `[ { "Action": [ "s3:GetObject" ], "Resource": [ "*" ], "Effect": "allow" } ]`,
			},
			ExpectedErrors: []string{"Unsupported value::spec.additionalPolicies[master][0].Effect"},
		},
	}
	for _, g := range grid {
		clusterSpec := &kops.ClusterSpec{
			KubernetesVersion:  "1.17.0",
			AdditionalPolicies: &g.Input,
			Subnets: []kops.ClusterSubnetSpec{
				{Name: "subnet1"},
			},
			EtcdClusters: []kops.EtcdClusterSpec{
				{
					Name: "main",
					Members: []kops.EtcdMemberSpec{
						{
							Name:          "us-test-1a",
							InstanceGroup: fi.String("master-us-test-1a"),
						},
					},
				},
			},
			IAM: &kops.IAMSpec{},
		}
		errs := validateClusterSpec(clusterSpec, &kops.Cluster{Spec: *clusterSpec}, field.NewPath("spec"))
		testErrors(t, g.Input, errs, g.ExpectedErrors)
	}
}

type caliInput struct {
	Calico *kops.CalicoNetworkingSpec
	Etcd   kops.EtcdClusterSpec
}

func Test_Validate_Calico(t *testing.T) {
	grid := []struct {
		Input          caliInput
		ExpectedErrors []string
	}{
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{},
				Etcd:   kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					TyphaReplicas: 3,
				},
				Etcd: kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					TyphaReplicas: -1,
				},
				Etcd: kops.EtcdClusterSpec{},
			},
			ExpectedErrors: []string{"Invalid value::calico.typhaReplicas"},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					MajorVersion: "v3",
				},
				Etcd: kops.EtcdClusterSpec{
					Version: "3.2.18",
				},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					MajorVersion: "v3",
				},
				Etcd: kops.EtcdClusterSpec{
					Version: "2.2.18",
				},
			},
			ExpectedErrors: []string{"Forbidden::calico.majorVersion"},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv4AutoDetectionMethod: "first-found",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv6AutoDetectionMethod: "first-found",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv4AutoDetectionMethod: "can-reach=8.8.8.8",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv6AutoDetectionMethod: "can-reach=2001:4860:4860::8888",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv4AutoDetectionMethod: "bogus",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
			ExpectedErrors: []string{"Invalid value::calico.ipv4AutoDetectionMethod"},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv6AutoDetectionMethod: "bogus",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
			ExpectedErrors: []string{"Invalid value::calico.ipv6AutoDetectionMethod"},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv6AutoDetectionMethod: "interface=",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
			ExpectedErrors: []string{"Invalid value::calico.ipv6AutoDetectionMethod"},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv4AutoDetectionMethod: "interface=en.*,eth0",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv6AutoDetectionMethod: "skip-interface=en.*,eth0",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv4AutoDetectionMethod: "interface=(,en1",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
			ExpectedErrors: []string{"Invalid value::calico.ipv4AutoDetectionMethod"},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv4AutoDetectionMethod: "interface=foo=bar",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
			ExpectedErrors: []string{"Invalid value::calico.ipv4AutoDetectionMethod"},
		},
		{
			Input: caliInput{
				Calico: &kops.CalicoNetworkingSpec{
					IPv4AutoDetectionMethod: "=en0,eth.*",
				},
				Etcd: kops.EtcdClusterSpec{},
			},
			ExpectedErrors: []string{"Invalid value::calico.ipv4AutoDetectionMethod"},
		},
	}
	for _, g := range grid {
		errs := validateNetworkingCalico(g.Input.Calico, g.Input.Etcd, field.NewPath("calico"))
		testErrors(t, g.Input, errs, g.ExpectedErrors)
	}
}

func Test_Validate_Cilium(t *testing.T) {
	grid := []struct {
		Cilium         kops.CiliumNetworkingSpec
		Spec           kops.ClusterSpec
		ExpectedErrors []string
	}{
		{
			Cilium: kops.CiliumNetworkingSpec{},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Ipam: "crd",
			},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				DisableMasquerade: true,
				Ipam:              "eni",
			},
			Spec: kops.ClusterSpec{
				CloudProvider: "aws",
			},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				DisableMasquerade: true,
				Ipam:              "eni",
			},
			Spec: kops.ClusterSpec{
				CloudProvider: "aws",
			},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Ipam: "foo",
			},
			ExpectedErrors: []string{"Unsupported value::cilium.ipam"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Ipam: "eni",
			},
			Spec: kops.ClusterSpec{
				CloudProvider: "aws",
			},
			ExpectedErrors: []string{"Forbidden::cilium.disableMasquerade"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				DisableMasquerade: true,
				Ipam:              "eni",
			},
			Spec: kops.ClusterSpec{
				CloudProvider: "gce",
			},
			ExpectedErrors: []string{"Forbidden::cilium.ipam"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "v1.0.0",
			},
			Spec: kops.ClusterSpec{
				KubernetesVersion: "1.11.0",
			},
			ExpectedErrors: []string{"Invalid value::cilium.version"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "v1.7.0",
			},
			Spec: kops.ClusterSpec{
				KubernetesVersion: "1.11.0",
			},
			ExpectedErrors: []string{"Forbidden::cilium.version"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "v1.7.0-rc1",
			},
			Spec: kops.ClusterSpec{
				KubernetesVersion: "1.11.0",
			},
			ExpectedErrors: []string{"Forbidden::cilium.version"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "v1.7.0",
			},
			Spec: kops.ClusterSpec{
				KubernetesVersion: "1.18.0",
			},
			ExpectedErrors: []string{"Forbidden::cilium.version"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "v1.7.0",
			},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "1.7.0",
			},
			ExpectedErrors: []string{"Invalid value::cilium.version"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "v1.7.0",
				Hubble: kops.HubbleSpec{
					Enabled: fi.Bool(true),
				},
			},
			ExpectedErrors: []string{"Forbidden::cilium.hubble.enabled"},
		},
		{
			Cilium: kops.CiliumNetworkingSpec{
				Version: "v1.8.0",
				Hubble: kops.HubbleSpec{
					Enabled: fi.Bool(true),
				},
			},
		},
	}
	for _, g := range grid {
		g.Spec.Networking = &kops.NetworkingSpec{
			Cilium: &g.Cilium,
		}
		if g.Spec.KubernetesVersion == "" {
			g.Spec.KubernetesVersion = "1.12.0"
		}
		cluster := &kops.Cluster{
			Spec: g.Spec,
		}
		errs := validateNetworkingCilium(cluster, g.Spec.Networking.Cilium, field.NewPath("cilium"))
		testErrors(t, g.Spec, errs, g.ExpectedErrors)
	}
}

func Test_Validate_RollingUpdate(t *testing.T) {
	grid := []struct {
		Input          kops.RollingUpdate
		OnMasterIG     bool
		ExpectedErrors []string
	}{
		{
			Input: kops.RollingUpdate{},
		},
		{
			Input: kops.RollingUpdate{
				MaxUnavailable: intStr(intstr.FromInt(0)),
			},
		},
		{
			Input: kops.RollingUpdate{
				MaxUnavailable: intStr(intstr.FromString("0%")),
			},
		},
		{
			Input: kops.RollingUpdate{
				MaxUnavailable: intStr(intstr.FromString("nope")),
			},
			ExpectedErrors: []string{"Invalid value::testField.maxUnavailable"},
		},
		{
			Input: kops.RollingUpdate{
				MaxUnavailable: intStr(intstr.FromInt(-1)),
			},
			ExpectedErrors: []string{"Invalid value::testField.maxUnavailable"},
		},
		{
			Input: kops.RollingUpdate{
				MaxUnavailable: intStr(intstr.FromString("-1%")),
			},
			ExpectedErrors: []string{"Invalid value::testField.maxUnavailable"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromInt(0)),
			},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("0%")),
			},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromInt(1)),
			},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("1%")),
			},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("nope")),
			},
			ExpectedErrors: []string{"Invalid value::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromInt(-1)),
			},
			ExpectedErrors: []string{"Invalid value::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("-1%")),
			},
			ExpectedErrors: []string{"Invalid value::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromInt(0)),
			},
			OnMasterIG: true,
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("0%")),
			},
			OnMasterIG: true,
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromInt(1)),
			},
			OnMasterIG:     true,
			ExpectedErrors: []string{"Forbidden::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("1%")),
			},
			OnMasterIG:     true,
			ExpectedErrors: []string{"Forbidden::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("nope")),
			},
			OnMasterIG:     true,
			ExpectedErrors: []string{"Invalid value::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromInt(-1)),
			},
			OnMasterIG:     true,
			ExpectedErrors: []string{"Forbidden::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxSurge: intStr(intstr.FromString("-1%")),
			},
			OnMasterIG:     true,
			ExpectedErrors: []string{"Forbidden::testField.maxSurge"},
		},
		{
			Input: kops.RollingUpdate{
				MaxUnavailable: intStr(intstr.FromInt(0)),
				MaxSurge:       intStr(intstr.FromInt(0)),
			},
			ExpectedErrors: []string{"Forbidden::testField.maxSurge"},
		},
	}
	for _, g := range grid {
		errs := validateRollingUpdate(&g.Input, field.NewPath("testField"), g.OnMasterIG)
		testErrors(t, g.Input, errs, g.ExpectedErrors)
	}
}

func intStr(i intstr.IntOrString) *intstr.IntOrString {
	return &i
}

func Test_Validate_NodeLocalDNS(t *testing.T) {
	grid := []struct {
		Input          kops.ClusterSpec
		ExpectedErrors []string
	}{
		{
			Input: kops.ClusterSpec{
				KubeProxy: &kops.KubeProxyConfig{
					ProxyMode: "iptables",
				},
				KubeDNS: &kops.KubeDNSConfig{
					Provider: "CoreDNS",
					NodeLocalDNS: &kops.NodeLocalDNSConfig{
						Enabled: fi.Bool(true),
					},
				},
			},
			ExpectedErrors: []string{},
		},
		{
			Input: kops.ClusterSpec{
				Kubelet: &kops.KubeletConfigSpec{
					ClusterDNS: "100.64.0.10",
				},
				KubeProxy: &kops.KubeProxyConfig{
					ProxyMode: "ipvs",
				},
				KubeDNS: &kops.KubeDNSConfig{
					Provider: "CoreDNS",
					NodeLocalDNS: &kops.NodeLocalDNSConfig{
						Enabled: fi.Bool(true),
					},
				},
			},
			ExpectedErrors: []string{"Forbidden::spec.kubelet.clusterDNS"},
		},
		{
			Input: kops.ClusterSpec{
				Kubelet: &kops.KubeletConfigSpec{
					ClusterDNS: "100.64.0.10",
				},
				KubeProxy: &kops.KubeProxyConfig{
					ProxyMode: "ipvs",
				},
				KubeDNS: &kops.KubeDNSConfig{
					Provider: "CoreDNS",
					NodeLocalDNS: &kops.NodeLocalDNSConfig{
						Enabled: fi.Bool(true),
					},
				},
				Networking: &kops.NetworkingSpec{
					Cilium: &kops.CiliumNetworkingSpec{},
				},
			},
			ExpectedErrors: []string{"Forbidden::spec.kubelet.clusterDNS"},
		},
		{
			Input: kops.ClusterSpec{
				Kubelet: &kops.KubeletConfigSpec{
					ClusterDNS: "169.254.20.10",
				},
				KubeProxy: &kops.KubeProxyConfig{
					ProxyMode: "iptables",
				},
				KubeDNS: &kops.KubeDNSConfig{
					Provider: "CoreDNS",
					NodeLocalDNS: &kops.NodeLocalDNSConfig{
						Enabled: fi.Bool(true),
						LocalIP: "169.254.20.10",
					},
				},
				Networking: &kops.NetworkingSpec{
					Cilium: &kops.CiliumNetworkingSpec{},
				},
			},
			ExpectedErrors: []string{},
		},
	}

	for _, g := range grid {
		errs := validateNodeLocalDNS(&g.Input, field.NewPath("spec"))
		testErrors(t, g.Input, errs, g.ExpectedErrors)
	}
}
