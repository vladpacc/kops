/*
Copyright 2020 The Kubernetes Authors.

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

package mockdns

import (
	"net/http/httptest"
	"sync"

	"github.com/gophercloud/gophercloud/openstack/dns/v2/recordsets"
	"github.com/gophercloud/gophercloud/openstack/dns/v2/zones"
	"k8s.io/kops/cloudmock/openstack"
)

// MockClient represents a mocked dns client
type MockClient struct {
	openstack.MockOpenstackServer
	mutex sync.Mutex

	zones      map[string]zones.Zone
	recordSets map[string]recordsets.RecordSet
}

// CreateClient will create a new mock dns client
func CreateClient() *MockClient {
	m := &MockClient{}
	m.Reset()
	m.SetupMux()
	m.mockZones()
	m.Server = httptest.NewServer(m.Mux)
	return m
}

// Reset will empty the state of the mock data
func (m *MockClient) Reset() {
	m.zones = make(map[string]zones.Zone)
	m.recordSets = make(map[string]recordsets.RecordSet)
}

// All returns a map of all resource IDs to their resources
func (m *MockClient) All() map[string]interface{} {
	all := make(map[string]interface{})
	for id, sg := range m.recordSets {
		all[id] = sg
	}
	return all
}
