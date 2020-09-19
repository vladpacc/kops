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

package cloudup

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"k8s.io/klog/v2"
	"k8s.io/kops"
	"k8s.io/kops/pkg/assets"
	"k8s.io/kops/util/pkg/architectures"
	"k8s.io/kops/util/pkg/hashing"
	"k8s.io/kops/util/pkg/mirrors"
)

const (
	defaultKopsBaseURL = "https://kubeupv2.s3.amazonaws.com/kops/%s/"

	// defaultKopsMirrorBase will be detected and automatically set to pull from the defaultKopsMirrors
	defaultKopsMirrorBase = "https://kubeupv2.s3.amazonaws.com/kops/%s/"
)

var kopsBaseURL *url.URL

// nodeUpAsset caches the nodeup download urls/hash
var nodeUpAsset map[architectures.Architecture]*MirroredAsset

// protokubeLocation caches the protokubeLocation url
var protokubeLocation map[architectures.Architecture]*url.URL

// protokubeHash caches the hash for protokube
var protokubeHash map[architectures.Architecture]*hashing.Hash

// BaseURL returns the base url for the distribution of kops - in particular for nodeup & docker images
func BaseURL() (*url.URL, error) {
	// returning cached value
	// Avoid repeated logging
	if kopsBaseURL != nil {
		klog.V(8).Infof("Using cached kopsBaseUrl url: %q", kopsBaseURL.String())
		return copyBaseURL(kopsBaseURL)
	}

	baseURLString := os.Getenv("KOPS_BASE_URL")
	var err error
	if baseURLString == "" {
		baseURLString = fmt.Sprintf(defaultKopsBaseURL, kops.Version)
		klog.V(8).Infof("Using default base url: %q", baseURLString)
		kopsBaseURL, err = url.Parse(baseURLString)
		if err != nil {
			return nil, fmt.Errorf("unable to parse %q as a url: %v", baseURLString, err)
		}
	} else {
		kopsBaseURL, err = url.Parse(baseURLString)
		if err != nil {
			return nil, fmt.Errorf("unable to parse env var KOPS_BASE_URL %q as a url: %v", baseURLString, err)
		}
		klog.Warningf("Using base url from KOPS_BASE_URL env var: %q", baseURLString)
	}

	return copyBaseURL(kopsBaseURL)
}

// copyBaseURL makes a copy of the base url or the path.Joins can append stuff to this URL
func copyBaseURL(base *url.URL) (*url.URL, error) {
	u, err := url.Parse(base.String())
	if err != nil {
		return nil, err
	}
	return u, nil
}

// SetKopsAssetsLocations sets the kops assets locations
// This func adds kops binary to the list of file assets, and stages the binary in the assets file repository
func SetKopsAssetsLocations(assetsBuilder *assets.AssetBuilder) error {
	for _, s := range []string{
		"linux/amd64/kops", "darwin/amd64/kops",
	} {
		_, _, err := KopsFileURL(s, assetsBuilder)
		if err != nil {
			return err
		}
	}
	return nil
}

// NodeUpAsset returns the asset for where nodeup should be downloaded
func NodeUpAsset(assetsBuilder *assets.AssetBuilder, arch architectures.Architecture) (*MirroredAsset, error) {
	if nodeUpAsset == nil {
		nodeUpAsset = make(map[architectures.Architecture]*MirroredAsset)
	}
	if nodeUpAsset[arch] != nil {
		// Avoid repeated logging
		klog.V(8).Infof("Using cached nodeup location for %s: %v", arch, nodeUpAsset[arch].Locations)
		return nodeUpAsset[arch], nil
	}
	// Use multi-arch env var, but fall back to well known env var
	env := os.Getenv(fmt.Sprintf("NODEUP_URL_%s", strings.ToUpper(string(arch))))
	if env == "" {
		env = os.Getenv("NODEUP_URL")
	}
	var err error
	var u *url.URL
	var hash *hashing.Hash
	if env == "" {
		u, hash, err = KopsFileURL(fmt.Sprintf("linux/%s/nodeup", arch), assetsBuilder)
		if err != nil {
			return nil, err
		}
		klog.V(8).Infof("Using default nodeup location for %s: %q", arch, u.String())
	} else {
		u, err = url.Parse(env)
		if err != nil {
			return nil, fmt.Errorf("unable to parse env var NODEUP_URL(_%s) %q as a url: %v", strings.ToUpper(string(arch)), env, err)
		}

		u, hash, err = assetsBuilder.RemapFileAndSHA(u)
		if err != nil {
			return nil, err
		}
		klog.Warningf("Using nodeup location from NODEUP_URL(_%s) env var: %q", strings.ToUpper(string(arch)), u.String())
	}

	asset := BuildMirroredAsset(u, hash)

	nodeUpAsset[arch] = asset

	return asset, nil
}

// TODO make this a container when hosted assets
// TODO does this support a docker as well??
// FIXME comments says this works with a docker already ... need to check on that

// ProtokubeImageSource returns the source for the docker image for protokube.
// Either a docker name (e.g. gcr.io/protokube:1.4), or a URL (https://...) in which case we download
// the contents of the url and docker load it
func ProtokubeImageSource(assetsBuilder *assets.AssetBuilder, arch architectures.Architecture) (*url.URL, *hashing.Hash, error) {
	if protokubeLocation == nil {
		protokubeLocation = make(map[architectures.Architecture]*url.URL)
	}
	if protokubeHash == nil {
		protokubeHash = make(map[architectures.Architecture]*hashing.Hash)
	}
	if nodeUpAsset[arch] != nil && protokubeHash[arch] != nil {
		// Avoid repeated logging
		klog.V(8).Infof("Using cached protokube location for %s: %q", arch, protokubeLocation[arch])
		return protokubeLocation[arch], protokubeHash[arch], nil
	}
	// Use multi-arch env var, but fall back to well known env var
	env := os.Getenv(fmt.Sprintf("PROTOKUBE_IMAGE_%s", strings.ToUpper(string(arch))))
	if env == "" {
		env = os.Getenv("PROTOKUBE_IMAGE")
	}
	var err error
	if env == "" {
		protokubeLocation[arch], protokubeHash[arch], err = KopsFileURL(fmt.Sprintf("images/protokube-%s.tar.gz", arch), assetsBuilder)
		if err != nil {
			return nil, nil, err
		}
		klog.V(8).Infof("Using default protokube location: %q", protokubeLocation[arch])
	} else {
		protokubeImageSource, err := url.Parse(env)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to parse env var PROTOKUBE_IMAGE(_%s) %q as a url: %v", strings.ToUpper(string(arch)), env, err)
		}

		protokubeLocation[arch], protokubeHash[arch], err = assetsBuilder.RemapFileAndSHA(protokubeImageSource)
		if err != nil {
			return nil, nil, err
		}
		klog.Warningf("Using protokube location from PROTOKUBE_IMAGE(_%s) env var: %q", strings.ToUpper(string(arch)), protokubeLocation[arch])
	}

	return protokubeLocation[arch], protokubeHash[arch], nil
}

// KopsFileURL returns the base url for the distribution of kops - in particular for nodeup & docker images
func KopsFileURL(file string, assetBuilder *assets.AssetBuilder) (*url.URL, *hashing.Hash, error) {
	base, err := BaseURL()
	if err != nil {
		return nil, nil, err
	}

	base.Path = path.Join(base.Path, file)

	fileURL, hash, err := assetBuilder.RemapFileAndSHA(base)
	if err != nil {
		return nil, nil, err
	}

	return fileURL, hash, nil
}

type MirroredAsset struct {
	Locations []string
	Hash      *hashing.Hash
}

// BuildMirroredAsset checks to see if this is a file under the standard base location, and if so constructs some mirror locations
func BuildMirroredAsset(u *url.URL, hash *hashing.Hash) *MirroredAsset {
	baseURLString := fmt.Sprintf(defaultKopsMirrorBase, kops.Version)
	if !strings.HasSuffix(baseURLString, "/") {
		baseURLString += "/"
	}

	a := &MirroredAsset{
		Hash: hash,
	}

	a.Locations = []string{u.String()}
	if strings.HasPrefix(u.String(), baseURLString) {
		if hash == nil {
			klog.Warningf("not using mirrors for asset %s as it does not have a known hash", u.String())
		} else {
			a.Locations = mirrors.FindUrlMirrors(u.String())
		}
	}

	return a
}

func (a *MirroredAsset) CompactString() string {
	var s string
	if a.Hash != nil {
		s = a.Hash.Hex()
	}
	s += "@" + strings.Join(a.Locations, ",")
	return s
}
