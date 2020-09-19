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
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/ghodss/yaml"
	"k8s.io/klog/v2"
	"k8s.io/kops/upup/pkg/fi/utils"

	"k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/apis/nodeup"
	"k8s.io/kops/pkg/model/resources"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/cloudup/awsup"
	"k8s.io/kops/util/pkg/architectures"
)

type NodeUpConfigBuilder interface {
	BuildConfig(ig *kops.InstanceGroup, apiserverAdditionalIPs []string) (*nodeup.Config, error)
}

// BootstrapScriptBuilder creates the bootstrap script
type BootstrapScriptBuilder struct {
	NodeUpSource        map[architectures.Architecture]string
	NodeUpSourceHash    map[architectures.Architecture]string
	NodeUpConfigBuilder NodeUpConfigBuilder
}

type BootstrapScript struct {
	Name     string
	ig       *kops.InstanceGroup
	builder  *BootstrapScriptBuilder
	resource fi.TaskDependentResource
	// alternateNameTasks are tasks that contribute api-server IP addresses.
	alternateNameTasks []fi.HasAddress
}

var _ fi.Task = &BootstrapScript{}
var _ fi.HasName = &BootstrapScript{}
var _ fi.HasDependencies = &BootstrapScript{}

// kubeEnv returns the nodeup config for the instance group
func (b *BootstrapScript) kubeEnv(ig *kops.InstanceGroup, c *fi.Context) (string, error) {
	var alternateNames []string

	for _, hasAddress := range b.alternateNameTasks {
		address, err := hasAddress.FindIPAddress(c)
		if err != nil {
			return "", fmt.Errorf("error finding address for %v: %v", hasAddress, err)
		}
		if address == nil {
			klog.Warningf("Task did not have an address: %v", hasAddress)
			continue
		}
		klog.V(8).Infof("Resolved alternateName %q for %q", *address, hasAddress)
		alternateNames = append(alternateNames, *address)
	}

	sort.Strings(alternateNames)
	config, err := b.builder.NodeUpConfigBuilder.BuildConfig(ig, alternateNames)
	if err != nil {
		return "", err
	}

	data, err := utils.YamlMarshal(config)
	if err != nil {
		return "", fmt.Errorf("error converting nodeup config to yaml: %v", err)
	}

	return string(data), nil
}

func (b *BootstrapScript) buildEnvironmentVariables(cluster *kops.Cluster) (map[string]string, error) {
	env := make(map[string]string)

	if os.Getenv("GOSSIP_DNS_CONN_LIMIT") != "" {
		env["GOSSIP_DNS_CONN_LIMIT"] = os.Getenv("GOSSIP_DNS_CONN_LIMIT")
	}

	if os.Getenv("S3_ENDPOINT") != "" {
		env["S3_ENDPOINT"] = os.Getenv("S3_ENDPOINT")
		env["S3_REGION"] = os.Getenv("S3_REGION")
		env["S3_ACCESS_KEY_ID"] = os.Getenv("S3_ACCESS_KEY_ID")
		env["S3_SECRET_ACCESS_KEY"] = os.Getenv("S3_SECRET_ACCESS_KEY")
	}

	if kops.CloudProviderID(cluster.Spec.CloudProvider) == kops.CloudProviderOpenstack {

		osEnvs := []string{
			"OS_TENANT_ID", "OS_TENANT_NAME", "OS_PROJECT_ID", "OS_PROJECT_NAME",
			"OS_PROJECT_DOMAIN_NAME", "OS_PROJECT_DOMAIN_ID",
			"OS_DOMAIN_NAME", "OS_DOMAIN_ID",
			"OS_AUTH_URL",
			"OS_REGION_NAME",
		}

		hasCCM := cluster.Spec.ExternalCloudControllerManager != nil
		appCreds := os.Getenv("OS_APPLICATION_CREDENTIAL_ID") != "" && os.Getenv("OS_APPLICATION_CREDENTIAL_SECRET") != ""
		if !hasCCM && appCreds {
			klog.Warning("application credentials only supported when using external cloud controller manager. Continuing with passwords.")
		}

		if hasCCM && appCreds {
			osEnvs = append(osEnvs,
				"OS_APPLICATION_CREDENTIAL_ID",
				"OS_APPLICATION_CREDENTIAL_SECRET",
			)
		} else {
			klog.Warning("exporting username and password. Consider using application credentials instead.")
			osEnvs = append(osEnvs,
				"OS_USERNAME",
				"OS_PASSWORD",
			)
		}

		// Pass in required credentials when using user-defined swift endpoint
		if os.Getenv("OS_AUTH_URL") != "" {
			for _, envVar := range osEnvs {
				env[envVar] = fmt.Sprintf("'%s'", os.Getenv(envVar))
			}
		}
	}

	if kops.CloudProviderID(cluster.Spec.CloudProvider) == kops.CloudProviderDO {
		doToken := os.Getenv("DIGITALOCEAN_ACCESS_TOKEN")
		if doToken != "" {
			env["DIGITALOCEAN_ACCESS_TOKEN"] = doToken
		}
	}

	if kops.CloudProviderID(cluster.Spec.CloudProvider) == kops.CloudProviderAWS {
		region, err := awsup.FindRegion(cluster)
		if err != nil {
			return nil, err
		}
		if region == "" {
			klog.Warningf("unable to determine cluster region")
		} else {
			env["AWS_REGION"] = region
		}
	}

	if kops.CloudProviderID(cluster.Spec.CloudProvider) == kops.CloudProviderALI {
		region := os.Getenv("OSS_REGION")
		if region != "" {
			env["OSS_REGION"] = os.Getenv("OSS_REGION")
		}

		aliID := os.Getenv("ALIYUN_ACCESS_KEY_ID")
		if aliID != "" {
			env["ALIYUN_ACCESS_KEY_ID"] = os.Getenv("ALIYUN_ACCESS_KEY_ID")
		}

		aliSecret := os.Getenv("ALIYUN_ACCESS_KEY_SECRET")
		if aliSecret != "" {
			env["ALIYUN_ACCESS_KEY_SECRET"] = os.Getenv("ALIYUN_ACCESS_KEY_SECRET")
		}
	}

	return env, nil
}

// ResourceNodeUp generates and returns a nodeup (bootstrap) script from a
// template file, substituting in specific env vars & cluster spec configuration
func (b *BootstrapScriptBuilder) ResourceNodeUp(c *fi.ModelBuilderContext, ig *kops.InstanceGroup) (*fi.ResourceHolder, error) {
	// Bastions can have AdditionalUserData, but if there isn't any skip this part
	if ig.IsBastion() && len(ig.Spec.AdditionalUserData) == 0 {
		templateResource, err := NewTemplateResource("nodeup", "", nil, nil)
		if err != nil {
			return nil, err
		}
		return fi.WrapResource(templateResource), nil
	}

	task := &BootstrapScript{
		Name:    ig.Name,
		ig:      ig,
		builder: b,
	}
	task.resource.Task = task
	c.AddTask(task)
	return fi.WrapResource(&task.resource), nil
}

func (b *BootstrapScript) GetName() *string {
	return &b.Name
}

func (b *BootstrapScript) GetDependencies(tasks map[string]fi.Task) []fi.Task {
	var deps []fi.Task

	for _, task := range tasks {
		if hasAddress, ok := task.(fi.HasAddress); ok && hasAddress.IsForAPIServer() {
			deps = append(deps, task)
			b.alternateNameTasks = append(b.alternateNameTasks, hasAddress)
		}
	}

	return deps
}

func (b *BootstrapScript) Run(c *fi.Context) error {
	functions := template.FuncMap{
		"NodeUpSourceAmd64": func() string {
			return b.builder.NodeUpSource[architectures.ArchitectureAmd64]
		},
		"NodeUpSourceHashAmd64": func() string {
			return b.builder.NodeUpSourceHash[architectures.ArchitectureAmd64]
		},
		"NodeUpSourceArm64": func() string {
			return b.builder.NodeUpSource[architectures.ArchitectureArm64]
		},
		"NodeUpSourceHashArm64": func() string {
			return b.builder.NodeUpSourceHash[architectures.ArchitectureArm64]
		},
		"KubeEnv": func() (string, error) {
			return b.kubeEnv(b.ig, c)
		},

		"EnvironmentVariables": func() (string, error) {
			env, err := b.buildEnvironmentVariables(c.Cluster)
			if err != nil {
				return "", err
			}

			// Sort keys to have a stable sequence of "export xx=xxx"" statements
			var keys []string
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			var b bytes.Buffer
			for _, k := range keys {
				b.WriteString(fmt.Sprintf("export %s=%s\n", k, env[k]))
			}
			return b.String(), nil
		},

		"ProxyEnv": func() string {
			return b.createProxyEnv(c.Cluster.Spec.EgressProxy)
		},

		"ClusterSpec": func() (string, error) {
			cs := c.Cluster.Spec

			spec := make(map[string]interface{})
			spec["cloudConfig"] = cs.CloudConfig
			spec["containerRuntime"] = cs.ContainerRuntime
			spec["containerd"] = cs.Containerd
			spec["docker"] = cs.Docker
			spec["kubeProxy"] = cs.KubeProxy
			spec["kubelet"] = cs.Kubelet

			if cs.NodeAuthorization != nil {
				spec["nodeAuthorization"] = cs.NodeAuthorization
			}
			if cs.KubeAPIServer != nil && cs.KubeAPIServer.EnableBootstrapAuthToken != nil {
				spec["kubeAPIServer"] = map[string]interface{}{
					"enableBootstrapAuthToken": cs.KubeAPIServer.EnableBootstrapAuthToken,
				}
			}

			if b.ig.IsMaster() {
				spec["encryptionConfig"] = cs.EncryptionConfig
				spec["etcdClusters"] = make(map[string]kops.EtcdClusterSpec)
				spec["kubeAPIServer"] = cs.KubeAPIServer
				spec["kubeControllerManager"] = cs.KubeControllerManager
				spec["kubeScheduler"] = cs.KubeScheduler
				spec["masterKubelet"] = cs.MasterKubelet

				for _, etcdCluster := range cs.EtcdClusters {
					c := kops.EtcdClusterSpec{
						Image:   etcdCluster.Image,
						Version: etcdCluster.Version,
					}
					// if the user has not specified memory or cpu allotments for etcd, do not
					// apply one.  Described in PR #6313.
					if etcdCluster.CPURequest != nil {
						c.CPURequest = etcdCluster.CPURequest
					}
					if etcdCluster.MemoryRequest != nil {
						c.MemoryRequest = etcdCluster.MemoryRequest
					}
					spec["etcdClusters"].(map[string]kops.EtcdClusterSpec)[etcdCluster.Name] = c
				}
			}

			hooks, err := b.getRelevantHooks(cs.Hooks, b.ig.Spec.Role)
			if err != nil {
				return "", err
			}
			if len(hooks) > 0 {
				spec["hooks"] = hooks
			}

			fileAssets, err := b.getRelevantFileAssets(cs.FileAssets, b.ig.Spec.Role)
			if err != nil {
				return "", err
			}
			if len(fileAssets) > 0 {
				spec["fileAssets"] = fileAssets
			}

			content, err := yaml.Marshal(spec)
			if err != nil {
				return "", fmt.Errorf("error converting cluster spec to yaml for inclusion within bootstrap script: %v", err)
			}
			return string(content), nil
		},

		"IGSpec": func() (string, error) {
			spec := make(map[string]interface{})

			hooks, err := b.getRelevantHooks(b.ig.Spec.Hooks, b.ig.Spec.Role)
			if err != nil {
				return "", err
			}
			if len(hooks) > 0 {
				spec["hooks"] = hooks
			}

			fileAssets, err := b.getRelevantFileAssets(b.ig.Spec.FileAssets, b.ig.Spec.Role)
			if err != nil {
				return "", err
			}
			if len(fileAssets) > 0 {
				spec["fileAssets"] = fileAssets
			}

			content, err := yaml.Marshal(spec)
			if err != nil {
				return "", fmt.Errorf("error converting instancegroup spec to yaml for inclusion within bootstrap script: %v", err)
			}
			return string(content), nil
		},
	}

	awsNodeUpTemplate, err := resources.AWSNodeUpTemplate(b.ig)
	if err != nil {
		return err
	}

	templateResource, err := NewTemplateResource("nodeup", awsNodeUpTemplate, functions, nil)
	if err != nil {
		return err
	}

	b.resource.Resource = templateResource
	return nil
}

// getRelevantHooks returns a list of hooks to be applied to the instance group,
// with the Manifest and ExecContainer Commands fingerprinted to reduce size
func (b *BootstrapScript) getRelevantHooks(allHooks []kops.HookSpec, role kops.InstanceGroupRole) ([]kops.HookSpec, error) {
	relevantHooks := []kops.HookSpec{}
	for _, hook := range allHooks {
		if len(hook.Roles) == 0 {
			relevantHooks = append(relevantHooks, hook)
			continue
		}
		for _, hookRole := range hook.Roles {
			if role == hookRole {
				relevantHooks = append(relevantHooks, hook)
				break
			}
		}
	}

	hooks := []kops.HookSpec{}
	if len(relevantHooks) > 0 {
		for _, hook := range relevantHooks {
			if hook.Manifest != "" {
				manifestFingerprint, err := b.computeFingerprint(hook.Manifest)
				if err != nil {
					return nil, err
				}
				hook.Manifest = manifestFingerprint + " (fingerprint)"
			}

			if hook.ExecContainer != nil && hook.ExecContainer.Command != nil {
				execContainerCommandFingerprint, err := b.computeFingerprint(strings.Join(hook.ExecContainer.Command[:], " "))
				if err != nil {
					return nil, err
				}

				execContainerAction := &kops.ExecContainerAction{
					Command:     []string{execContainerCommandFingerprint + " (fingerprint)"},
					Environment: hook.ExecContainer.Environment,
					Image:       hook.ExecContainer.Image,
				}
				hook.ExecContainer = execContainerAction
			}

			hook.Roles = nil
			hooks = append(hooks, hook)
		}
	}

	return hooks, nil
}

// getRelevantFileAssets returns a list of file assets to be applied to the
// instance group, with the Content fingerprinted to reduce size
func (b *BootstrapScript) getRelevantFileAssets(allFileAssets []kops.FileAssetSpec, role kops.InstanceGroupRole) ([]kops.FileAssetSpec, error) {
	relevantFileAssets := []kops.FileAssetSpec{}
	for _, fileAsset := range allFileAssets {
		if len(fileAsset.Roles) == 0 {
			relevantFileAssets = append(relevantFileAssets, fileAsset)
			continue
		}
		for _, fileAssetRole := range fileAsset.Roles {
			if role == fileAssetRole {
				relevantFileAssets = append(relevantFileAssets, fileAsset)
				break
			}
		}
	}

	fileAssets := []kops.FileAssetSpec{}
	if len(relevantFileAssets) > 0 {
		for _, fileAsset := range relevantFileAssets {
			if fileAsset.Content != "" {
				contentFingerprint, err := b.computeFingerprint(fileAsset.Content)
				if err != nil {
					return nil, err
				}
				fileAsset.Content = contentFingerprint + " (fingerprint)"
			}

			fileAsset.Roles = nil
			fileAssets = append(fileAssets, fileAsset)
		}
	}

	return fileAssets, nil
}

// computeFingerprint takes a string and returns a base64 encoded fingerprint
func (b *BootstrapScript) computeFingerprint(content string) (string, error) {
	hasher := sha1.New()

	if _, err := hasher.Write([]byte(content)); err != nil {
		return "", fmt.Errorf("error computing fingerprint hash: %v", err)
	}

	return base64.StdEncoding.EncodeToString(hasher.Sum(nil)), nil
}

func (b *BootstrapScript) createProxyEnv(ps *kops.EgressProxySpec) string {
	var buffer bytes.Buffer

	if ps != nil && ps.HTTPProxy.Host != "" {
		var httpProxyURL string

		// TODO double check that all the code does this
		// TODO move this into a validate so we can enforce the string syntax
		if !strings.HasPrefix(ps.HTTPProxy.Host, "http://") {
			httpProxyURL = "http://"
		}

		if ps.HTTPProxy.Port != 0 {
			httpProxyURL += ps.HTTPProxy.Host + ":" + strconv.Itoa(ps.HTTPProxy.Port)
		} else {
			httpProxyURL += ps.HTTPProxy.Host
		}

		// Set env variables for base environment
		buffer.WriteString(`echo "http_proxy=` + httpProxyURL + `" >> /etc/environment` + "\n")
		buffer.WriteString(`echo "https_proxy=` + httpProxyURL + `" >> /etc/environment` + "\n")
		buffer.WriteString(`echo "no_proxy=` + ps.ProxyExcludes + `" >> /etc/environment` + "\n")
		buffer.WriteString(`echo "NO_PROXY=` + ps.ProxyExcludes + `" >> /etc/environment` + "\n")

		// Load the proxy environment variables
		buffer.WriteString("while read in; do export $in; done < /etc/environment\n")

		// Set env variables for package manager depending on OS Distribution (N/A for Flatcar)
		// Note: Nodeup will source the `/etc/environment` file within docker config in the correct location
		buffer.WriteString("case `cat /proc/version` in\n")
		buffer.WriteString("*[Dd]ebian*)\n")
		buffer.WriteString(`  echo "Acquire::http::Proxy \"${http_proxy}\";" > /etc/apt/apt.conf.d/30proxy ;;` + "\n")
		buffer.WriteString("*[Uu]buntu*)\n")
		buffer.WriteString(`  echo "Acquire::http::Proxy \"${http_proxy}\";" > /etc/apt/apt.conf.d/30proxy ;;` + "\n")
		buffer.WriteString("*[Rr]ed[Hh]at*)\n")
		buffer.WriteString(`  echo "proxy=${http_proxy}" >> /etc/yum.conf ;;` + "\n")
		buffer.WriteString("esac\n")

		// Set env variables for systemd
		buffer.WriteString(`echo "DefaultEnvironment=\"http_proxy=${http_proxy}\" \"https_proxy=${http_proxy}\"`)
		buffer.WriteString(` \"NO_PROXY=${no_proxy}\" \"no_proxy=${no_proxy}\""`)
		buffer.WriteString(" >> /etc/systemd/system.conf\n")

		// Restart stuff
		buffer.WriteString("systemctl daemon-reload\n")
		buffer.WriteString("systemctl daemon-reexec\n")
	}
	return buffer.String()
}
