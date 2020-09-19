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

package iam

import (
	"encoding/json"
	"fmt"

	"k8s.io/kops/pkg/apis/kops"
)

// ParseStatements parses JSON into a list of Statements
func ParseStatements(policy string) ([]*Statement, error) {
	statements := make([]*Statement, 0)
	if err := json.Unmarshal([]byte(policy), &statements); err != nil {
		return nil, fmt.Errorf("error parsing IAM statements: %v", err)
	}
	return statements, nil
}

type IAMModelContext struct {
	// AWSAccountID holds the 12 digit AWS account ID, when running on AWS
	AWSAccountID string
	// AWSPartition defines the partition of the AWS account, typically "aws", "aws-cn", or "aws-us-gov"
	AWSPartition string

	Cluster *kops.Cluster
}

// IAMNameForServiceAccountRole determines the name of the IAM Role and Instance Profile to use for the service-account role
func (b *IAMModelContext) IAMNameForServiceAccountRole(role Subject) (string, error) {
	serviceAccount, ok := role.ServiceAccount()
	if !ok {
		return "", fmt.Errorf("role %v does not have ServiceAccount", role)
	}

	return serviceAccount.Name + "." + serviceAccount.Namespace + ".sa." + b.ClusterName(), nil
}

// ClusterName returns the cluster name
func (b *IAMModelContext) ClusterName() string {
	return b.Cluster.ObjectMeta.Name
}
